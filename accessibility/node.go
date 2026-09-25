package accessibility

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/godbus/dbus/v5"
)

func (b *dbusBackend) readNode(ctx context.Context, id, parent NodeID, maxText int, allowSensitive bool) (Node, error) {
	if ctx == nil {
		return Node{}, errors.New("accessibility: nil context")
	}
	expected := id.Generation
	if _, err := b.object(id); err != nil {
		return Node{}, err
	}
	node := Node{ID: id, Parent: parent}
	if item, ok := b.cachedItem(id); ok {
		node.Name = item.Name
		node.Description = item.Description
		node.ChildCount = int(maxInt32(item.ChildCount))
		node.RoleID = item.Role
		node.Role = roleName(item.Role)
		node.Interfaces = append([]string(nil), item.Interfaces...)
		b.applyStates(item.States, &node)
	} else {
		_ = b.property(ctx, id, accessibleIface, "Name", &node.Name)
		_ = b.property(ctx, id, accessibleIface, "Description", &node.Description)
		b.readNodeRoleAndCount(ctx, id, &node)
		node.Interfaces = b.readNodeInterfaces(ctx, id)
		b.readNodeStates(ctx, id, &node)
	}
	b.readNodeComponent(ctx, id, node.Interfaces, &node)
	b.readNodeText(ctx, id, node.Interfaces, maxText, &node)
	b.readNodeAttributes(ctx, id, &node)
	b.readNodeOptional(ctx, id, node.Interfaces, &node)
	if !allowSensitive && hasSensitiveState(node.States) {
		redactSensitiveNode(&node)
	}
	if err := b.generationError(expected); err != nil {
		return Node{}, err
	}
	return node, nil
}

func maxInt32(value int32) int32 {
	if value < 0 {
		return 0
	}
	return value
}

func redactSensitiveNode(node *Node) {
	if node == nil {
		return
	}
	node.Redacted = true
	node.Text = ""
	// Value.Current can itself be sensitive (for example a password or
	// protected spin control). Mutation permission is intentionally separate
	// from reading this field.
	node.Value = nil
	for key := range node.Attributes {
		lower := strings.ToLower(key)
		if lower == "value" || strings.Contains(lower, "text") || strings.Contains(lower, "password") || strings.Contains(lower, "secret") || strings.Contains(lower, "protected") {
			delete(node.Attributes, key)
		}
	}
}

func hasSensitiveState(states []string) bool {
	for _, state := range states {
		if strings.EqualFold(state, "sensitive") || strings.EqualFold(state, "protected") {
			return true
		}
	}
	return false
}

func (b *dbusBackend) readNodeRoleAndCount(ctx context.Context, id NodeID, node *Node) {
	var count int32
	if err := b.property(ctx, id, accessibleIface, "ChildCount", &count); err == nil && count > 0 {
		node.ChildCount = int(count)
	}
	var role uint32
	if err := b.call(ctx, id, accessibleIface+".GetRole", nil, &role); err == nil {
		node.RoleID = role
		node.Role = roleName(role)
	}
}

func (b *dbusBackend) readNodeInterfaces(ctx context.Context, id NodeID) []string {
	var interfaces []string
	if err := b.call(ctx, id, accessibleIface+".GetInterfaces", nil, &interfaces); err != nil {
		return nil
	}
	return interfaces
}

func (b *dbusBackend) readNodeStates(ctx context.Context, id NodeID, node *Node) {
	var states []uint32
	if err := b.call(ctx, id, accessibleIface+".GetState", nil, &states); err != nil {
		return
	}
	b.applyStates(states, node)
}

func (b *dbusBackend) applyStates(states []uint32, node *Node) {
	for word, bits := range states {
		for bit := uint32(0); bit < 32; bit++ {
			if bits&(uint32(1)<<bit) == 0 {
				continue
			}
			state := uint32(word*32) + bit
			if name := stateName(state); name != "" {
				node.States = append(node.States, name)
			}
			switch state {
			case 8:
				node.Enabled = true
			case 12:
				node.Focused = true
			case 25:
				node.Showing = true
			case 30:
				node.Visible = true
			}
		}
	}
}

func (b *dbusBackend) readNodeComponent(ctx context.Context, id NodeID, interfaces []string, node *Node) {
	if !contains(interfaces, componentIface) {
		return
	}
	var extents struct{ X, Y, Width, Height int32 }
	if err := b.call(ctx, id, componentIface+".GetExtents", []any{uint32(0)}, &extents); err != nil {
		return
	}
	node.Bounds = Rect{X: int(extents.X), Y: int(extents.Y), Width: int(extents.Width), Height: int(extents.Height)}
	node.HasBounds = true
}

func (b *dbusBackend) readNodeText(ctx context.Context, id NodeID, interfaces []string, maxText int, node *Node) {
	if !contains(interfaces, textIface) {
		return
	}
	var chars int32
	if err := b.property(ctx, id, textIface, "CharacterCount", &chars); err != nil || chars <= 0 {
		return
	}
	if maxText <= 0 {
		maxText = defaultMaxText
	}
	if chars > int32(maxText) {
		chars = int32(maxText)
	}
	if err := b.call(ctx, id, textIface+".GetText", []any{int32(0), chars}, &node.Text); err != nil {
		return
	}
	node.Text, node.TextTruncated = truncateUTF8(node.Text, maxText)
}

func (b *dbusBackend) readNodeAttributes(ctx context.Context, id NodeID, node *Node) {
	var attrs map[string]string
	if err := b.call(ctx, id, accessibleIface+".GetAttributes", nil, &attrs); err == nil {
		node.Attributes = attrs
	}
}

// readNodeOptional performs best-effort reads for the optional AT-SPI
// interfaces advertised by GetInterfaces. Every field is independent: a
// provider that implements only part of an interface must not make the whole
// node unreadable.
func (b *dbusBackend) readNodeOptional(ctx context.Context, id NodeID, interfaces []string, node *Node) { //nolint:gocyclo // each optional interface is intentionally isolated.
	if contains(interfaces, valueIface) {
		value := &ValueInfo{}
		if err := b.propertyFloat(ctx, id, valueIface, "CurrentValue", &value.Current); err == nil {
			for _, field := range []struct {
				name   string
				target *float64
			}{{"MinimumValue", &value.Minimum}, {"MaximumValue", &value.Maximum}, {"MinimumIncrement", &value.MinimumIncrement}} {
				if optionalErr := b.propertyFloat(ctx, id, valueIface, field.name, field.target); optionalErr != nil {
					node.Warnings = append(node.Warnings, fmt.Sprintf("%s.%s: %v", valueIface, field.name, optionalErr))
				}
			}
			node.Value = value
		} else {
			node.Warnings = append(node.Warnings, fmt.Sprintf("%s.CurrentValue: %v", valueIface, err))
		}
	}
	if contains(interfaces, actionIface) {
		if actions, err := b.actionMetadata(ctx, id); err == nil {
			node.Actions = actions
			for i := range node.Actions {
				name, nameErr := b.actionName(ctx, id, node.Actions[i].Index)
				if nameErr != nil {
					node.Warnings = append(node.Warnings, fmt.Sprintf("%s.GetName(%d): %v", actionIface, node.Actions[i].Index, nameErr))
					continue
				}
				node.Actions[i].Name = name
			}
		} else {
			node.Warnings = append(node.Warnings, fmt.Sprintf("%s.GetActions: %v", actionIface, err))
		}
	}
	if contains(interfaces, selectionIface) {
		var count int32
		if err := b.call(ctx, id, selectionIface+".GetSelectedChildCount", nil, &count); err == nil {
			node.Selection = &SelectionInfo{SelectedChildCount: maxInt32(count)}
		} else {
			node.Warnings = append(node.Warnings, fmt.Sprintf("%s.GetSelectedChildCount: %v", selectionIface, err))
		}
	}
	if contains(interfaces, tableIface) {
		var rows, columns int32
		rowsErr := b.property(ctx, id, tableIface, "NRows", &rows)
		columnsErr := b.property(ctx, id, tableIface, "NColumns", &columns)
		if rowsErr == nil || columnsErr == nil {
			node.Table = &TableInfo{Rows: maxInt32(rows), Columns: maxInt32(columns)}
		}
		if rowsErr != nil {
			node.Warnings = append(node.Warnings, fmt.Sprintf("%s.NRows: %v", tableIface, rowsErr))
		}
		if columnsErr != nil {
			node.Warnings = append(node.Warnings, fmt.Sprintf("%s.NColumns: %v", tableIface, columnsErr))
		}
	}
	if contains(interfaces, documentIface) {
		document := &DocumentInfo{}
		got := false
		if b.property(ctx, id, documentIface, "Locale", &document.Locale) == nil {
			got = true
		}
		if b.property(ctx, id, documentIface, "CurrentPageNumber", &document.CurrentPageNumber) == nil {
			got = true
		}
		if b.property(ctx, id, documentIface, "PageCount", &document.PageCount) == nil {
			got = true
		}
		if got {
			node.Document = document
		}
		if !got {
			node.Warnings = append(node.Warnings, fmt.Sprintf("%s: no readable document properties", documentIface))
		}
	}
	if len(interfaces) > 0 {
		b.readNodeRelations(ctx, id, node)
	}
}

func (b *dbusBackend) readNodeRelations(ctx context.Context, id NodeID, node *Node) {
	var relations []struct {
		RelationType uint32
		Targets      []objectRef
	}
	if err := b.call(ctx, id, accessibleIface+".GetRelationSet", nil, &relations); err != nil {
		node.Warnings = append(node.Warnings, fmt.Sprintf("%s.GetRelationSet: %v", accessibleIface, err))
		return
	}
	for _, relation := range relations {
		name := relationName(relation.RelationType)
		if name == "" || len(relation.Targets) == 0 {
			continue
		}
		if node.Relations == nil {
			node.Relations = make(map[string][]NodeID)
		}
		for _, target := range relation.Targets {
			if !target.null() {
				node.Relations[name] = append(node.Relations[name], b.refID(target))
			}
		}
	}
}

func (b *dbusBackend) propertyFloat(ctx context.Context, id NodeID, iface, name string, out *float64) error {
	var variant dbus.Variant
	if err := b.call(ctx, id, propertiesIface+".Get", []any{iface, name}, &variant); err != nil {
		return err
	}
	switch value := variant.Value().(type) {
	case float64:
		*out = value
	case float32:
		*out = float64(value)
	case int32:
		*out = float64(value)
	case uint32:
		*out = float64(value)
	default:
		return fmt.Errorf("accessibility: property %s.%s has type %T", iface, name, value)
	}
	return nil
}

func (b *dbusBackend) call(ctx context.Context, id NodeID, method string, args []any, out any) error {
	obj, err := b.object(id)
	if err != nil {
		return err
	}
	if args == nil {
		args = []any{}
	}
	err = obj.CallWithContext(ctx, method, 0, args...).Store(out)
	if err != nil && isDisconnectedDBusError(err) {
		return fmt.Errorf("%w: %w", ErrDisconnected, err)
	}
	return err
}

func (b *dbusBackend) property(ctx context.Context, id NodeID, iface, name string, out any) error {
	var variant dbus.Variant
	if err := b.call(ctx, id, propertiesIface+".Get", []any{iface, name}, &variant); err != nil {
		return err
	}
	value := variant.Value()
	switch dst := out.(type) {
	case *string:
		v, ok := value.(string)
		if !ok {
			return fmt.Errorf("accessibility: property %s.%s has type %T", iface, name, value)
		}
		*dst = v
	case *int32:
		v, ok := value.(int32)
		if !ok {
			return fmt.Errorf("accessibility: property %s.%s has type %T", iface, name, value)
		}
		*dst = v
	default:
		return fmt.Errorf("accessibility: unsupported property destination %T", out)
	}
	return nil
}

func contains(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

func roleName(role uint32) string {
	// Keep this table aligned with the AT-SPI/ATK role enum. Firefox publishes
	// the Browser Console through the cache path, so callers do not always get
	// the provider's GetRoleName string. In particular, section (85) and
	// landmark (110) are used to bound console prompt discovery around the
	// large output subtree.
	roles := map[uint32]string{
		7:   "check-box",
		11:  "combo-box",
		16:  "dialog",
		23:  "frame",
		25:  "html-container",
		28:  "internal-frame",
		31:  "list",
		32:  "list-item",
		39:  "panel",
		43:  "button",
		61:  "text",
		69:  "window",
		73:  "paragraph",
		75:  "application",
		78:  "embedded",
		79:  "entry",
		82:  "document-frame",
		83:  "heading",
		84:  "page",
		85:  "section",
		88:  "link",
		98:  "list-box",
		99:  "grouping",
		106: "audio",
		110: "landmark",
		129: "push-button",
		130: "switch",
	}
	if name, ok := roles[role]; ok {
		return name
	}
	return fmt.Sprintf("role-%d", role)
}

func stateName(state uint32) string {
	states := map[uint32]string{1: "active", 3: "busy", 4: "checked", 5: "collapsed", 7: "editable", 8: "enabled", 9: "expandable", 10: "expanded", 11: "focusable", 12: "focused", 20: "pressed", 22: "selectable", 23: "selected", 24: "sensitive", 25: "showing", 26: "indeterminate", 27: "stale", 28: "transient", 29: "vertical", 30: "visible", 33: "required", 34: "protected"}
	return states[state]
}

func relationName(relation uint32) string {
	relations := map[uint32]string{
		0: "label-for", 1: "labelled-by", 2: "controller-for", 3: "controlled-by",
		4: "member-of", 5: "tooltip-for", 6: "described-by", 7: "description-for",
		8: "node-child-of", 9: "flows-to", 10: "flows-from", 11: "subwindow-of",
		12: "embeds", 13: "embedded-by", 14: "popup-for", 15: "parent-window-of",
		16: "details", 17: "details-for", 18: "error-message", 19: "error-for",
	}
	return relations[relation]
}

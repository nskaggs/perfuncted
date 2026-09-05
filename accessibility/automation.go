package accessibility

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/godbus/dbus/v5"
)

func (b *dbusBackend) mutation(ctx context.Context, id NodeID, iface, method string, args ...any) error {
	if err := mutationContext(ctx, method); err != nil {
		return err
	}
	if b != nil && b.callOverride != nil {
		if err := b.validateHandle(id); err != nil {
			return err
		}
		_, err := b.callOverride(ctx, id, iface+"."+method, args)
		return normalizeMutationError(iface, method, err)
	}
	obj, err := b.object(id)
	if err != nil {
		return err
	}
	call := obj.CallWithContext(ctx, iface+"."+method, 0, args...)
	if call.Err != nil {
		return normalizeMutationError(iface, method, call.Err)
	}
	return nil
}

func (b *dbusBackend) mutationBool(ctx context.Context, id NodeID, iface, method string, args ...any) error {
	if err := mutationContext(ctx, method); err != nil {
		return err
	}
	if b != nil && b.callOverride != nil {
		if err := b.validateHandle(id); err != nil {
			return err
		}
		result, err := b.callOverride(ctx, id, iface+"."+method, args)
		if normalizedErr := normalizeMutationError(iface, method, err); normalizedErr != nil {
			return normalizedErr
		}
		accepted, ok := result.(bool)
		if !ok {
			return fmt.Errorf("accessibility: %s.%s returned malformed result %T", iface, method, result)
		}
		if !accepted {
			return ErrMutationRejected
		}
		return nil
	}
	obj, err := b.object(id)
	if err != nil {
		return err
	}
	var accepted bool
	call := obj.CallWithContext(ctx, iface+"."+method, 0, args...)
	if err := call.Store(&accepted); err != nil {
		return normalizeMutationError(iface, method, err)
	}
	if !accepted {
		return ErrMutationRejected
	}
	return nil
}

func mutationContext(ctx context.Context, method string) error {
	if ctx == nil {
		return fmt.Errorf("accessibility: %s: nil context", method)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func normalizeMutationError(iface, method string, err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "UnknownMethod") || strings.Contains(err.Error(), "UnknownInterface") {
		return fmt.Errorf("%w: %s.%s", ErrUnsupported, iface, method)
	}
	return fmt.Errorf("accessibility: %s.%s: %w", iface, method, err)
}

func (b *dbusBackend) actions(ctx context.Context, id NodeID) ([]Action, error) {
	if err := mutationContext(ctx, "GetActions"); err != nil {
		return nil, err
	}
	if b != nil && b.callOverride != nil {
		if err := b.validateHandle(id); err != nil {
			return nil, err
		}
		result, err := b.callOverride(ctx, id, actionIface+".GetActions", nil)
		if normalizedErr := normalizeMutationError(actionIface, "GetActions", err); normalizedErr != nil {
			return nil, normalizedErr
		}
		if actions, ok := result.([]Action); ok {
			return actions, nil
		}
		return nil, fmt.Errorf("accessibility: action fixture returned %T", result)
	}
	if _, err := b.object(id); err != nil {
		return nil, err
	}
	var wire []struct {
		Name        string
		Description string
		KeyBinding  string
	}
	if err := b.call(ctx, id, actionIface+".GetActions", nil, &wire); err != nil {
		if strings.Contains(err.Error(), "UnknownMethod") || strings.Contains(err.Error(), "UnknownInterface") {
			return nil, fmt.Errorf("%w: action metadata", ErrUnsupported)
		}
		return nil, err
	}
	actions := make([]Action, len(wire))
	for i, action := range wire {
		actions[i] = Action{Index: int32(i), Name: action.Name, Description: action.Description, KeyBinding: action.KeyBinding}
	}
	return actions, nil
}

func (b *dbusBackend) InvokeAction(ctx context.Context, id NodeID, index int32) error {
	actions, err := b.actions(ctx, id)
	if err != nil {
		return err
	}
	if index < 0 || int(index) >= len(actions) {
		return fmt.Errorf("%w: %d", ErrInvalidAction, index)
	}
	return b.mutationBool(ctx, id, actionIface, "DoAction", index)
}

func (b *dbusBackend) InvokeActionByName(ctx context.Context, id NodeID, name string) error {
	_, err := b.InvokeActionByNameExact(ctx, id, name)
	return err
}

// InvokeActionByNameExact selects one action from a single metadata read,
// invokes that action's stable index, and returns the exact selected metadata.
// Keeping this primitive separate preserves the existing low-level method
// while allowing higher-level receipts to describe the physical invocation.
func (b *dbusBackend) InvokeActionByNameExact(ctx context.Context, id NodeID, name string) (Action, error) {
	actions, err := b.actions(ctx, id)
	if err != nil {
		return Action{}, err
	}
	want := strings.TrimSpace(name)
	if want == "" {
		return Action{}, ErrNotFound
	}
	var chosen Action
	found := false
	for _, action := range actions {
		if strings.EqualFold(strings.TrimSpace(action.Name), want) {
			if found {
				return Action{}, fmt.Errorf("%w: action %q", ErrAmbiguous, want)
			}
			chosen = action
			found = true
		}
	}
	if !found {
		return Action{}, ErrNotFound
	}
	// Invoke the index selected from this metadata read. Re-reading actions
	// could select a different operation if the provider changes between calls.
	if err := b.mutationBool(ctx, id, actionIface, "DoAction", chosen.Index); err != nil {
		return Action{}, err
	}
	return chosen, nil
}

func (b *dbusBackend) InvokeDefaultAction(ctx context.Context, id NodeID) (Action, error) {
	actions, err := b.actions(ctx, id)
	if err != nil {
		return Action{}, err
	}
	if len(actions) == 0 {
		return Action{}, ErrNotFound
	}
	chosen := actions[0]
	if err := b.mutationBool(ctx, id, actionIface, "DoAction", chosen.Index); err != nil {
		return Action{}, err
	}
	return chosen, nil
}

func (b *dbusBackend) GrabFocus(ctx context.Context, id NodeID) error {
	return b.mutationBool(ctx, id, componentIface, "GrabFocus")
}

func (b *dbusBackend) ScrollTo(ctx context.Context, id NodeID, scrollType ScrollType) error {
	if scrollType > ScrollAnyWhere {
		return fmt.Errorf("accessibility: invalid scroll type %d", scrollType)
	}
	return b.mutationBool(ctx, id, componentIface, "ScrollTo", uint32(scrollType))
}

func (b *dbusBackend) ScrollToPoint(ctx context.Context, id NodeID, coordType CoordType, x, y int) error {
	if coordType > CoordTypeParent {
		return fmt.Errorf("accessibility: invalid coordinate type %d", coordType)
	}
	if x < -2147483648 || x > 2147483647 || y < -2147483648 || y > 2147483647 {
		return fmt.Errorf("accessibility: scroll coordinates out of range")
	}
	return b.mutationBool(ctx, id, componentIface, "ScrollToPoint", uint32(coordType), int32(x), int32(y))
}

func (b *dbusBackend) SetPosition(ctx context.Context, id NodeID, x, y int, coordType CoordType) error {
	if coordType > CoordTypeParent {
		return fmt.Errorf("accessibility: invalid coordinate type %d", coordType)
	}
	if x < -2147483648 || x > 2147483647 || y < -2147483648 || y > 2147483647 {
		return fmt.Errorf("accessibility: position coordinates out of range")
	}
	return b.mutationBool(ctx, id, componentIface, "SetPosition", int32(x), int32(y), uint32(coordType))
}

func (b *dbusBackend) SetSize(ctx context.Context, id NodeID, width, height int) error {
	if width < 0 || width > 2147483647 || height < 0 || height > 2147483647 {
		return fmt.Errorf("accessibility: size out of range")
	}
	return b.mutationBool(ctx, id, componentIface, "SetSize", int32(width), int32(height))
}

func (b *dbusBackend) SetExtents(ctx context.Context, id NodeID, x, y, width, height int, coordType CoordType) error {
	if coordType > CoordTypeParent {
		return fmt.Errorf("accessibility: invalid coordinate type %d", coordType)
	}
	if x < -2147483648 || x > 2147483647 || y < -2147483648 || y > 2147483647 || width < 0 || width > 2147483647 || height < 0 || height > 2147483647 {
		return fmt.Errorf("accessibility: extents out of range")
	}
	return b.mutationBool(ctx, id, componentIface, "SetExtents", int32(x), int32(y), int32(width), int32(height), uint32(coordType))
}

func (b *dbusBackend) SetCurrentValue(ctx context.Context, id NodeID, value float64) error {
	if err := mutationContext(ctx, "SetCurrentValue"); err != nil {
		return err
	}
	if b != nil && b.callOverride != nil {
		if err := b.validateHandle(id); err != nil {
			return err
		}
		_, err := b.callOverride(ctx, id, propertiesIface+".Set", []any{valueIface, "CurrentValue", dbus.MakeVariant(value)})
		return normalizeMutationError(valueIface, "CurrentValue", err)
	}
	obj, err := b.object(id)
	if err != nil {
		return err
	}
	if err := obj.CallWithContext(ctx, propertiesIface+".Set", 0, valueIface, "CurrentValue", dbus.MakeVariant(value)).Err; err != nil {
		return normalizeMutationError(valueIface, "CurrentValue", err)
	}
	return nil
}

func (b *dbusBackend) SetValue(ctx context.Context, id NodeID, value float64) error {
	return b.SetCurrentValue(ctx, id, value)
}

func (b *dbusBackend) SetTextContents(ctx context.Context, id NodeID, text string) error {
	return b.mutationBool(ctx, id, editableTextIface, "SetTextContents", text)
}

func (b *dbusBackend) ReplaceText(ctx context.Context, id NodeID, start, end int32, text string) error {
	if start < 0 || end < start {
		return fmt.Errorf("accessibility: invalid text range %d:%d", start, end)
	}
	if err := b.DeleteText(ctx, id, start, end); err != nil {
		return err
	}
	return b.InsertText(ctx, id, start, text)
}

func (b *dbusBackend) InsertText(ctx context.Context, id NodeID, offset int32, text string) error {
	if offset < 0 {
		return fmt.Errorf("accessibility: invalid text offset %d", offset)
	}
	return b.mutationBool(ctx, id, editableTextIface, "InsertText", offset, text, int32(utf8.RuneCountInString(text)))
}

func (b *dbusBackend) DeleteText(ctx context.Context, id NodeID, start, end int32) error {
	if err := validTextRange(start, end); err != nil {
		return err
	}
	return b.mutationBool(ctx, id, editableTextIface, "DeleteText", start, end)
}

func (b *dbusBackend) CopyText(ctx context.Context, id NodeID, start, end int32) error {
	if err := validTextRange(start, end); err != nil {
		return err
	}
	return b.mutation(ctx, id, editableTextIface, "CopyText", start, end)
}

func (b *dbusBackend) CutText(ctx context.Context, id NodeID, start, end int32) error {
	if err := validTextRange(start, end); err != nil {
		return err
	}
	return b.mutationBool(ctx, id, editableTextIface, "CutText", start, end)
}

func validTextRange(start, end int32) error {
	if start < 0 || end < start {
		return fmt.Errorf("accessibility: invalid text range %d:%d", start, end)
	}
	return nil
}

func (b *dbusBackend) PasteText(ctx context.Context, id NodeID, position int32) error {
	return b.mutationBool(ctx, id, editableTextIface, "PasteText", position)
}

func (b *dbusBackend) SetCaretOffset(ctx context.Context, id NodeID, offset int32) error {
	if offset < 0 {
		return fmt.Errorf("accessibility: invalid caret offset %d", offset)
	}
	return b.mutationBool(ctx, id, textIface, "SetCaretOffset", offset)
}

func (b *dbusBackend) SetTextSelection(ctx context.Context, id NodeID, selection, start, end int32) error {
	return b.mutationBool(ctx, id, textIface, "SetSelection", selection, start, end)
}

func (b *dbusBackend) AddTextSelection(ctx context.Context, id NodeID, start, end int32) error {
	return b.mutationBool(ctx, id, textIface, "AddSelection", start, end)
}

func (b *dbusBackend) RemoveTextSelection(ctx context.Context, id NodeID, selection int32) error {
	return b.mutationBool(ctx, id, textIface, "RemoveSelection", selection)
}

type documentTextSelectionWire struct {
	StartObject   objectRef
	StartOffset   int32
	EndObject     objectRef
	EndOffset     int32
	StartIsActive bool
}

// SetTextSelections uses the AT-SPI 2.52 Document wire shape
// a((so)i(so)ib). The provider validates that both endpoints are descendants
// of the document; this layer rejects malformed or stale endpoint handles
// before their raw D-Bus references are encoded.
func (b *dbusBackend) SetTextSelections(ctx context.Context, id NodeID, selections []DocumentTextSelection) error {
	if err := b.validateHandle(id); err != nil {
		return err
	}
	wire := make([]documentTextSelectionWire, len(selections))
	for i, selection := range selections {
		if err := b.validateHandle(selection.StartObject); err != nil {
			return fmt.Errorf("accessibility: document selection %d start: %w", i, err)
		}
		if err := b.validateHandle(selection.EndObject); err != nil {
			return fmt.Errorf("accessibility: document selection %d end: %w", i, err)
		}
		if selection.StartOffset < 0 || selection.EndOffset < 0 {
			return fmt.Errorf("accessibility: document selection %d has negative offset", i)
		}
		wire[i] = documentTextSelectionWire{
			StartObject:   objectRef{BusName: selection.StartObject.BusName, ObjectPath: dbus.ObjectPath(selection.StartObject.ObjectPath)},
			StartOffset:   selection.StartOffset,
			EndObject:     objectRef{BusName: selection.EndObject.BusName, ObjectPath: dbus.ObjectPath(selection.EndObject.ObjectPath)},
			EndOffset:     selection.EndOffset,
			StartIsActive: selection.StartIsActive,
		}
	}
	return b.mutationBool(ctx, id, documentIface, "SetTextSelections", wire)
}

func (b *dbusBackend) SelectChild(ctx context.Context, id NodeID, index int32) error {
	return b.mutationBool(ctx, id, selectionIface, "SelectChild", index)
}

func (b *dbusBackend) DeselectChild(ctx context.Context, id NodeID, index int32) error {
	return b.mutationBool(ctx, id, selectionIface, "DeselectChild", index)
}

func (b *dbusBackend) SelectAll(ctx context.Context, id NodeID) error {
	return b.mutationBool(ctx, id, selectionIface, "SelectAll")
}

func (b *dbusBackend) ClearSelection(ctx context.Context, id NodeID) error {
	return b.mutationBool(ctx, id, selectionIface, "ClearSelection")
}

func (b *dbusBackend) DeselectAll(ctx context.Context, id NodeID) error {
	// AT-SPI has no DeselectAll method. Keep this ergonomic alias wired to the
	// standard ClearSelection operation.
	return b.ClearSelection(ctx, id)
}

func (b *dbusBackend) DeselectSelectedChild(ctx context.Context, id NodeID) error {
	return b.mutationBool(ctx, id, selectionIface, "DeselectSelectedChild")
}

func (b *dbusBackend) SelectRow(ctx context.Context, id NodeID, row int32) error {
	return b.mutationBool(ctx, id, tableIface, "AddRowSelection", row)
}

func (b *dbusBackend) DeselectRow(ctx context.Context, id NodeID, row int32) error {
	return b.mutationBool(ctx, id, tableIface, "RemoveRowSelection", row)
}

func (b *dbusBackend) SelectColumn(ctx context.Context, id NodeID, column int32) error {
	return b.mutationBool(ctx, id, tableIface, "AddColumnSelection", column)
}

func (b *dbusBackend) DeselectColumn(ctx context.Context, id NodeID, column int32) error {
	return b.mutationBool(ctx, id, tableIface, "RemoveColumnSelection", column)
}

var _ Automation = (*dbusBackend)(nil)

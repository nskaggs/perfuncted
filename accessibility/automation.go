package accessibility

import (
	"context"
	"fmt"
	"strings"

	"github.com/godbus/dbus/v5"
)

func (b *dbusBackend) mutation(ctx context.Context, id NodeID, iface, method string, args ...any) error {
	if ctx == nil {
		return fmt.Errorf("accessibility: %s: nil context", method)
	}
	obj, err := b.object(id)
	if err != nil {
		return err
	}
	call := obj.CallWithContext(ctx, iface+"."+method, 0, args...)
	if call.Err != nil {
		if strings.Contains(call.Err.Error(), "UnknownMethod") || strings.Contains(call.Err.Error(), "UnknownInterface") {
			return fmt.Errorf("%w: %s.%s", ErrUnsupported, iface, method)
		}
		return fmt.Errorf("accessibility: %s.%s: %w", iface, method, call.Err)
	}
	return nil
}

func (b *dbusBackend) mutationBool(ctx context.Context, id NodeID, iface, method string, args ...any) error {
	if ctx == nil {
		return fmt.Errorf("accessibility: %s: nil context", method)
	}
	obj, err := b.object(id)
	if err != nil {
		return err
	}
	var accepted bool
	call := obj.CallWithContext(ctx, iface+"."+method, 0, args...)
	if err := call.Store(&accepted); err != nil {
		if strings.Contains(err.Error(), "UnknownMethod") || strings.Contains(err.Error(), "UnknownInterface") {
			return fmt.Errorf("%w: %s.%s", ErrUnsupported, iface, method)
		}
		return fmt.Errorf("accessibility: %s.%s: %w", iface, method, err)
	}
	if !accepted {
		return ErrMutationRejected
	}
	return nil
}

func (b *dbusBackend) actions(ctx context.Context, id NodeID) ([]Action, error) {
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
	actions, err := b.actions(ctx, id)
	if err != nil {
		return err
	}
	want := strings.TrimSpace(name)
	if want == "" {
		return ErrNotFound
	}
	match := -1
	for _, action := range actions {
		if strings.EqualFold(strings.TrimSpace(action.Name), want) {
			if match >= 0 {
				return fmt.Errorf("%w: action %q", ErrAmbiguous, want)
			}
			match = int(action.Index)
		}
	}
	if match < 0 {
		return ErrNotFound
	}
	return b.InvokeAction(ctx, id, int32(match))
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
	return b.mutationBool(ctx, id, componentIface, "ScrollTo", uint32(scrollType))
}

func (b *dbusBackend) ScrollToPoint(ctx context.Context, id NodeID, scrollType ScrollType, x, y int) error {
	return b.mutationBool(ctx, id, componentIface, "ScrollToPoint", uint32(scrollType), int32(x), int32(y))
}

func (b *dbusBackend) SetCurrentValue(ctx context.Context, id NodeID, value float64) error {
	if ctx == nil {
		return fmt.Errorf("accessibility: SetCurrentValue: nil context")
	}
	obj, err := b.object(id)
	if err != nil {
		return err
	}
	if err := obj.CallWithContext(ctx, propertiesIface+".Set", 0, valueIface, "CurrentValue", dbus.MakeVariant(value)).Err; err != nil {
		if strings.Contains(err.Error(), "UnknownMethod") || strings.Contains(err.Error(), "UnknownInterface") {
			return fmt.Errorf("%w: %s.CurrentValue", ErrUnsupported, valueIface)
		}
		return fmt.Errorf("accessibility: %s.CurrentValue: %w", valueIface, err)
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
	return b.mutationBool(ctx, id, editableTextIface, "InsertText", offset, text, int32(len([]byte(text))))
}

func (b *dbusBackend) DeleteText(ctx context.Context, id NodeID, start, end int32) error {
	return b.mutationBool(ctx, id, editableTextIface, "DeleteText", start, end)
}

func (b *dbusBackend) CopyText(ctx context.Context, id NodeID, start, end int32) error {
	return b.mutation(ctx, id, editableTextIface, "CopyText", start, end)
}

func (b *dbusBackend) CutText(ctx context.Context, id NodeID, start, end int32) error {
	return b.mutationBool(ctx, id, editableTextIface, "CutText", start, end)
}

func (b *dbusBackend) PasteText(ctx context.Context, id NodeID, position int32) error {
	return b.mutationBool(ctx, id, editableTextIface, "PasteText", position)
}

func (b *dbusBackend) SetCaretOffset(ctx context.Context, id NodeID, offset int32) error {
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
	return b.mutationBool(ctx, id, selectionIface, "DeselectAll")
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

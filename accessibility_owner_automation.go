package perfuncted

import (
	"context"

	"github.com/nskaggs/perfuncted/accessibility"
)

func (o *accessibilityBackendOwner) withAutomation(ctx context.Context, call func(accessibility.Automation, context.Context) error) error {
	return o.withBackendContext(ctx, func(backend accessibility.Backend, callCtx context.Context) error {
		automation, ok := backend.(accessibility.Automation)
		if !ok {
			return accessibility.ErrUnsupported
		}
		return call(automation, callCtx)
	})
}

func (o *accessibilityBackendOwner) InvokeAction(ctx context.Context, id accessibility.NodeID, index int32) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error { return a.InvokeAction(ctx, id, index) })
}

func (o *accessibilityBackendOwner) InvokeActionByName(ctx context.Context, id accessibility.NodeID, name string) (accessibility.Action, error) {
	var result accessibility.Action
	err := o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error {
		var err error
		result, err = a.InvokeActionByName(ctx, id, name)
		return err
	})
	return result, err
}

func (o *accessibilityBackendOwner) InvokeDefaultAction(ctx context.Context, id accessibility.NodeID) (accessibility.Action, error) {
	var result accessibility.Action
	err := o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error {
		var err error
		result, err = a.InvokeDefaultAction(ctx, id)
		return err
	})
	return result, err
}

func (o *accessibilityBackendOwner) GrabFocus(ctx context.Context, id accessibility.NodeID) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error { return a.GrabFocus(ctx, id) })
}

func (o *accessibilityBackendOwner) ScrollTo(ctx context.Context, id accessibility.NodeID, kind accessibility.ScrollType) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error { return a.ScrollTo(ctx, id, kind) })
}

func (o *accessibilityBackendOwner) ScrollToPoint(ctx context.Context, id accessibility.NodeID, kind accessibility.CoordType, x, y int) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error {
		return a.ScrollToPoint(ctx, id, kind, x, y)
	})
}

func (o *accessibilityBackendOwner) SetPosition(ctx context.Context, id accessibility.NodeID, x, y int, kind accessibility.CoordType) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error { return a.SetPosition(ctx, id, x, y, kind) })
}

func (o *accessibilityBackendOwner) SetSize(ctx context.Context, id accessibility.NodeID, width, height int) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error { return a.SetSize(ctx, id, width, height) })
}

func (o *accessibilityBackendOwner) SetExtents(ctx context.Context, id accessibility.NodeID, x, y, width, height int, kind accessibility.CoordType) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error {
		return a.SetExtents(ctx, id, x, y, width, height, kind)
	})
}

func (o *accessibilityBackendOwner) SetValue(ctx context.Context, id accessibility.NodeID, value float64) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error { return a.SetValue(ctx, id, value) })
}

func (o *accessibilityBackendOwner) SetTextContents(ctx context.Context, id accessibility.NodeID, value string) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error { return a.SetTextContents(ctx, id, value) })
}

func (o *accessibilityBackendOwner) InsertText(ctx context.Context, id accessibility.NodeID, offset int32, value string) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error {
		return a.InsertText(ctx, id, offset, value)
	})
}

func (o *accessibilityBackendOwner) DeleteText(ctx context.Context, id accessibility.NodeID, start, end int32) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error { return a.DeleteText(ctx, id, start, end) })
}

func (o *accessibilityBackendOwner) CopyText(ctx context.Context, id accessibility.NodeID, start, end int32) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error { return a.CopyText(ctx, id, start, end) })
}

func (o *accessibilityBackendOwner) CutText(ctx context.Context, id accessibility.NodeID, start, end int32) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error { return a.CutText(ctx, id, start, end) })
}

func (o *accessibilityBackendOwner) PasteText(ctx context.Context, id accessibility.NodeID, position int32) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error { return a.PasteText(ctx, id, position) })
}

func (o *accessibilityBackendOwner) SetCaretOffset(ctx context.Context, id accessibility.NodeID, offset int32) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error { return a.SetCaretOffset(ctx, id, offset) })
}

func (o *accessibilityBackendOwner) SetTextSelection(ctx context.Context, id accessibility.NodeID, selection, start, end int32) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error {
		return a.SetTextSelection(ctx, id, selection, start, end)
	})
}

func (o *accessibilityBackendOwner) AddTextSelection(ctx context.Context, id accessibility.NodeID, start, end int32) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error {
		return a.AddTextSelection(ctx, id, start, end)
	})
}

func (o *accessibilityBackendOwner) RemoveTextSelection(ctx context.Context, id accessibility.NodeID, selection int32) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error {
		return a.RemoveTextSelection(ctx, id, selection)
	})
}

func (o *accessibilityBackendOwner) SetTextSelections(ctx context.Context, id accessibility.NodeID, selections []accessibility.DocumentTextSelection) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error {
		return a.SetTextSelections(ctx, id, selections)
	})
}

func (o *accessibilityBackendOwner) SelectChild(ctx context.Context, id accessibility.NodeID, index int32) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error { return a.SelectChild(ctx, id, index) })
}

func (o *accessibilityBackendOwner) DeselectChild(ctx context.Context, id accessibility.NodeID, index int32) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error { return a.DeselectChild(ctx, id, index) })
}

func (o *accessibilityBackendOwner) SelectAll(ctx context.Context, id accessibility.NodeID) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error { return a.SelectAll(ctx, id) })
}

func (o *accessibilityBackendOwner) ClearSelection(ctx context.Context, id accessibility.NodeID) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error { return a.ClearSelection(ctx, id) })
}

func (o *accessibilityBackendOwner) DeselectSelectedChild(ctx context.Context, id accessibility.NodeID) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error { return a.DeselectSelectedChild(ctx, id) })
}

func (o *accessibilityBackendOwner) SelectRow(ctx context.Context, id accessibility.NodeID, row int32) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error { return a.SelectRow(ctx, id, row) })
}

func (o *accessibilityBackendOwner) DeselectRow(ctx context.Context, id accessibility.NodeID, row int32) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error { return a.DeselectRow(ctx, id, row) })
}

func (o *accessibilityBackendOwner) SelectColumn(ctx context.Context, id accessibility.NodeID, column int32) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error { return a.SelectColumn(ctx, id, column) })
}

func (o *accessibilityBackendOwner) DeselectColumn(ctx context.Context, id accessibility.NodeID, column int32) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error { return a.DeselectColumn(ctx, id, column) })
}

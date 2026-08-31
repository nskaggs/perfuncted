import Clutter from 'gi://Clutter';
import GLib from 'gi://GLib';

function now() {
    return Number(GLib.get_monotonic_time?.() ?? Date.now() * 1000);
}

function keyState(pressed) {
    return pressed ? Clutter.KeyState.PRESSED : Clutter.KeyState.RELEASED;
}

function buttonState(pressed) {
    return pressed ? Clutter.ButtonState.PRESSED : Clutter.ButtonState.RELEASED;
}

export class Input {
    constructor() {
        const seat = global.backend.get_default_seat();
        if (!seat?.create_virtual_device)
            throw new Error('GNOME virtual input devices are unavailable');
        this._keyboard = seat.create_virtual_device(Clutter.InputDeviceType.KEYBOARD_DEVICE);
        this._pointer = seat.create_virtual_device(Clutter.InputDeviceType.POINTER_DEVICE);
    }

    key(keyval, pressed) {
        this._keyboard.notify_keyval(now(), Number(keyval), keyState(pressed));
    }

    text(text, clipboard) {
        // Mutter's keyval injection only works when the current layout has a
        // matching keycode. Use the compositor clipboard and a normal paste
        // shortcut so arbitrary Unicode does not depend on that layout.
        if (!clipboard || typeof clipboard.setText !== 'function')
            throw new Error('GNOME clipboard is required for arbitrary text input');
        clipboard.setText(text);
        this.paste();
    }

    paste() {
        const control = 0xffe3;
        const v = 0x76;
        this.key(control, true);
        try {
            this.key(v, true);
            this.key(v, false);
        } finally {
            this.key(control, false);
        }
    }

    pointerMove(x, y) {
        this._pointer.notify_absolute_motion(now(), Number(x), Number(y));
    }

    pointerButton(button, pressed) {
        this._pointer.notify_button(now(), Number(button), buttonState(pressed));
    }

    scroll(axis, amount) {
        const value = Number(amount);
        if (!Number.isSafeInteger(value) || value === 0)
            throw new Error('scroll amount must be a non-zero integer number of clicks');
        if (axis !== 'horizontal' && axis !== 'vertical')
            throw new Error(`unsupported scroll axis ${axis}`);
        if (typeof this._pointer.notify_discrete_scroll !== 'function')
            throw new Error('GNOME discrete scrolling is unavailable');
        const source = Clutter.ScrollSource?.WHEEL ?? 0;
        const direction = axis === 'horizontal' ?
            (value < 0 ? Clutter.ScrollDirection.LEFT : Clutter.ScrollDirection.RIGHT) :
            (value < 0 ? Clutter.ScrollDirection.UP : Clutter.ScrollDirection.DOWN);
        const count = Math.abs(value);
        for (let i = 0; i < count; i++)
            this._pointer.notify_discrete_scroll(now(), direction, source);
    }

    pointerLocation() {
        const pointer = global.get_pointer();
        return [Number(pointer[0]), Number(pointer[1])];
    }

}

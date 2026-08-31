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

    text(text) {
        // notify_keyval() is appropriate for ordinary text whose keysyms are
        // present in the active layout. Keep this path as real key input so a
        // held modifier remains held (for example Ctrl+C).
        const keyvals = [];
        for (const character of text) {
            const codepoint = character.codePointAt(0);
            let keyval = codepoint;
            if (character === '\n' || character === '\r')
                keyval = 0xff0d;
            else if (character === '\t')
                keyval = 0xff09;
            else if (character === '\b')
                keyval = 0xff08;
            else if (codepoint < 0x20 || codepoint > 0x7e)
                throw new Error('GNOME direct text input only supports ASCII; use paste for Unicode');
            keyvals.push(keyval);
        }
        for (const keyval of keyvals) {
            this.key(keyval, true);
            this.key(keyval, false);
        }
    }

    pasteText(text, clipboard) {
        // Mutter has no compositor-level text-commit primitive. Clipboard
        // paste is the Unicode fallback, kept separate from direct typing so
        // it cannot consume modifiers belonging to the caller's key syntax.
        if (!clipboard || typeof clipboard.setText !== 'function')
            throw new Error('GNOME clipboard is required for Unicode text input');
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

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

function unicodeKeyval(codepoint) {
    if (typeof Clutter.unicode_to_keysym === 'function')
        return Clutter.unicode_to_keysym(codepoint);
    return codepoint < 0x100 ? codepoint : codepoint | 0x01000000;
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
        for (const character of text) {
            const keyval = unicodeKeyval(character.codePointAt(0));
            this.key(keyval, true);
            this.key(keyval, false);
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
        const source = Clutter.ScrollSource?.WHEEL ?? 0;
        const finishFlags = Clutter.ScrollFinishFlags?.NONE ?? 0;
        if (typeof this._pointer.notify_scroll_continuous === 'function') {
            this._pointer.notify_scroll_continuous(
                now(), axis === 'horizontal' ? value : 0, axis === 'vertical' ? value : 0,
                source, finishFlags);
            return;
        }
        const direction = axis === 'horizontal' ?
            (value < 0 ? Clutter.ScrollDirection.LEFT : Clutter.ScrollDirection.RIGHT) :
            (value < 0 ? Clutter.ScrollDirection.UP : Clutter.ScrollDirection.DOWN);
        const count = Math.max(1, Math.round(Math.abs(value)));
        for (let i = 0; i < count; i++)
            this._pointer.notify_discrete_scroll(now(), direction, source);
    }

    pointerLocation() {
        const pointer = global.get_pointer();
        return [Number(pointer[0]), Number(pointer[1])];
    }

    sync() {
        // Virtual-input notifications are delivered by the Shell main loop;
        // reaching this method is the ordering barrier for the D-Bus caller.
    }
}

import GLib from 'gi://GLib';
import St from 'gi://St';

export class Clipboard {
    constructor() {
        this._clipboard = St.Clipboard.get_default();
    }

    getText() {
        let text = '';
        const loop = GLib.MainLoop.new(null, false);
        this._clipboard.get_text(St.ClipboardType.CLIPBOARD, (_clipboard, value) => {
            text = value ?? '';
            loop.quit();
        });
        loop.run();
        return text;
    }

    setText(text) {
        this._clipboard.set_text(St.ClipboardType.CLIPBOARD, text);
    }
}

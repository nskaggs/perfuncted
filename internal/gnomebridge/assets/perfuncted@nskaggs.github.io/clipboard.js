import St from 'gi://St';

export class Clipboard {
    constructor() {
        this._clipboard = St.Clipboard.get_default();
        if (!this._clipboard)
            throw new Error('GNOME clipboard is unavailable');
    }

    getText() {
        return new Promise((resolve, reject) => {
            try {
                this._clipboard.get_text(St.ClipboardType.CLIPBOARD, (_clipboard, value) => {
                    resolve(value ?? '');
                });
            } catch (error) {
                reject(error);
            }
        });
    }

    setText(text) {
        this._clipboard.set_text(St.ClipboardType.CLIPBOARD, text);
    }
}

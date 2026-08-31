import {Extension} from 'resource:///org/gnome/shell/extensions/extension.js';

import {BridgeService} from './service.js';

export default class PerfunctedExtension extends Extension {
    enable() {
        this._service = new BridgeService();
        this._service.enable();
    }

    disable() {
        this._service?.disable();
        this._service = null;
    }
}

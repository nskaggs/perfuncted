import Meta from 'gi://Meta';

const WINDOW_INFO_SIGNATURE = '(ssssiiiiibbbb)';
const WINDOW_CHANGE_SIGNALS = [
    'notify::title',
    'notify::gtk-application-id',
    'notify::wm-class',
    'notify::minimized',
    'notify::maximized-horizontally',
    'notify::maximized-vertically',
    'notify::fullscreen',
    'position-changed',
    'size-changed',
];

function notFound(id) {
    const error = new Error(`window not found: ${id}`);
    error.name = 'io.github.nskaggs.perfuncted.Gnome1.Error.NotFound';
    return error;
}

function allWindows() {
    if (typeof global.display.list_all_windows === 'function')
        return global.display.list_all_windows();
    return global.get_window_actors().map(actor => actor.meta_window ?? actor.get_meta_window());
}

function visibleWindows() {
    return allWindows().filter(window => window &&
        typeof window.is_skip_taskbar === 'function' && !window.is_skip_taskbar());
}

function rectangle(window) {
    const rect = window.get_frame_rect();
    return [rect.x, rect.y, rect.width, rect.height];
}

function windowInfo(window) {
    const [x, y, width, height] = rectangle(window);
    const focused = global.display.get_focus_window?.() === window;
    const maximized = window.maximized_horizontally && window.maximized_vertically;
    const fullscreen = typeof window.is_fullscreen === 'function' ?
        window.is_fullscreen() : Boolean(window.fullscreen);
    const appId = typeof window.get_gtk_application_id === 'function' ?
        window.get_gtk_application_id() : '';
    return [
        String(window.get_stable_sequence()),
        window.get_title?.() ?? '',
        appId ?? '',
        window.get_wm_class?.() ?? '',
        Number(window.get_pid?.() ?? 0),
        x, y, width, height,
        focused,
        Boolean(window.minimized),
        Boolean(maximized),
        Boolean(fullscreen),
    ];
}

function findWindow(id) {
    return visibleWindows().find(window => String(window.get_stable_sequence()) === String(id));
}

export class Windows {
    constructor(emit) {
        this._emit = emit;
        this._signals = [];
    }

    enable() {
        const display = global.display;
        this._connect(display, 'window-created', (_display, window) => {
            if (!window?.is_skip_taskbar?.()) {
                this._watchWindow(window);
                this._emit('WindowAdded', WINDOW_INFO_SIGNATURE, windowInfo(window));
            }
        });
        this._connect(display, 'notify::focus-window', () => {
            const focused = display.get_focus_window?.();
            this._emit('FocusChanged', 's', focused ? String(focused.get_stable_sequence()) : '');
            if (focused)
                this._emit('WindowChanged', WINDOW_INFO_SIGNATURE, windowInfo(focused));
        });
        for (const window of visibleWindows())
            this._watchWindow(window);
    }

    disable() {
        for (const [object, signal] of this._signals.splice(0))
            object.disconnect(signal);
    }

    _connect(object, signal, callback) {
        if (!object?.connect)
            return;
        try {
            this._signals.push([object, object.connect(signal, callback)]);
        } catch (error) {
            console.warn(`perfuncted: GNOME window signal ${signal} unavailable: ${error}`);
        }
    }

    _watchWindow(window) {
        this._connect(window, 'unmanaged', () => {
            this._emit('WindowRemoved', 's', String(window.get_stable_sequence()));
        });
        const changed = () => {
            if (visibleWindows().includes(window))
                this._emit('WindowChanged', WINDOW_INFO_SIGNATURE, windowInfo(window));
        };
        for (const signal of WINDOW_CHANGE_SIGNALS)
            this._connect(window, signal, changed);
    }

    list() {
        return visibleWindows().map(windowInfo);
    }

    get(id) {
        const window = findWindow(id);
        if (!window)
            throw notFound(id);
        return windowInfo(window);
    }

    active() {
        const window = global.display.get_focus_window?.();
        return window && !window.is_skip_taskbar?.() ? windowInfo(window) :
            ['', '', '', '', 0, 0, 0, 0, 0, false, false, false, false];
    }

    act(id, callback) {
        const window = findWindow(id);
        if (!window)
            throw notFound(id);
        callback(window);
    }

    activate(id) { this.act(id, window => window.activate(global.get_current_time())); }
    move(id, x, y) { this.act(id, window => window.move_frame(true, x, y)); }
    resize(id, width, height) {
        this.act(id, window => {
            const rect = window.get_frame_rect();
            window.move_resize_frame(true, rect.x, rect.y, width, height);
        });
    }
    minimize(id) { this.act(id, window => window.minimize()); }
    maximize(id) { this.act(id, window => window.maximize(Meta.MaximizeFlags.BOTH)); }
    restore(id) {
        this.act(id, window => {
            window.unminimize();
            window.unmaximize(Meta.MaximizeFlags.BOTH);
        });
    }
    fullscreen(id) { this.act(id, window => window.make_fullscreen()); }
    unfullscreen(id) { this.act(id, window => window.unmake_fullscreen()); }
    close(id) { this.act(id, window => window.delete(global.get_current_time())); }
}

export {WINDOW_INFO_SIGNATURE};

import Gio from 'gi://Gio';
import Mtk from 'gi://Mtk';
import Shell from 'gi://Shell';

function bridgeError(kind, message) {
    const error = new Error(message);
    error.name = `io.github.nskaggs.perfuncted.Gnome1.Error.${kind}`;
    return error;
}

function resolveFD(handle, fdList) {
    const index = Number(handle);
    if (!fdList || typeof fdList.get_length !== 'function' || typeof fdList.get !== 'function')
        throw bridgeError('Unsupported', 'screenshot call did not include a Unix FD list');
    if (!Number.isInteger(index) || index < 0 || index >= fdList.get_length())
        throw bridgeError('InvalidArgument', `screenshot FD handle ${handle} is out of range`);
    try {
        const fd = fdList.get(index);
        if (fd < 0)
            throw new Error('Unix FD list returned an invalid descriptor');
        return fd;
    } catch (error) {
        throw bridgeError('Unsupported', `cannot duplicate screenshot FD: ${error}`);
    }
}

function captureRect(x, y, width, height) {
    const stage = global.get_stage?.() ?? global.stage;
    if (!stage || typeof stage.get_capture_final_size !== 'function')
        throw bridgeError('Unsupported', 'GNOME capture geometry is unavailable');
    const rect = new Mtk.Rectangle({x, y, width, height});
    const result = stage.get_capture_final_size(rect);
    if (!Array.isArray(result) || result.length < 4 || result[0] === false)
        throw bridgeError('Unsupported', 'GNOME could not determine capture geometry');
    const pixelWidth = Number(result[1]);
    const pixelHeight = Number(result[2]);
    const scale = Number(result[3]);
    if (!Number.isInteger(pixelWidth) || pixelWidth <= 0 ||
        !Number.isInteger(pixelHeight) || pixelHeight <= 0 ||
        !Number.isFinite(scale) || scale <= 0)
        throw bridgeError('Unsupported', 'GNOME returned invalid capture geometry');
    return [x, y, width, height, pixelWidth, pixelHeight, scale];
}

function fullScreenRect() {
    const width = Number(global.get_screen_width?.());
    const height = Number(global.get_screen_height?.());
    if (!Number.isInteger(width) || width <= 0 || !Number.isInteger(height) || height <= 0)
        throw bridgeError('Unsupported', 'GNOME screen geometry is unavailable');
    return {x: 0, y: 0, width, height};
}

function runScreenshot(fd, start, metadata) {
    const stream = new Gio.UnixOutputStream({fd, close_fd: true});
    const screenshot = new Shell.Screenshot();
    return new Promise((resolve, reject) => {
        let completed = false;
        const complete = error => {
            if (completed)
                return;
            completed = true;
            try {
                // close_fd owns the duplicated descriptor returned by
                // UnixFDList.get().
                stream.close(null);
            } catch (closeError) {
                error ??= closeError;
            }
            if (error)
                reject(error);
            else
                resolve(metadata);
        };
        try {
            start.begin(screenshot, stream, (_object, result) => {
                try {
                    const outcome = start.finish(screenshot, result);
                    if (outcome === false || (Array.isArray(outcome) && outcome[0] === false))
                        complete(bridgeError('Unsupported', 'GNOME screenshot operation failed'));
                    else
                        complete(null);
                } catch (error) {
                    complete(error);
                }
            });
        } catch (error) {
            complete(error);
        }
    });
}

export class Screen {
    captureFull(handle, fdList) {
        const rect = fullScreenRect();
        const metadata = captureRect(rect.x, rect.y, rect.width, rect.height);
        const fd = resolveFD(handle, fdList);
        return runScreenshot(fd, {
            begin: (s, output, callback) => s.screenshot(false, output, callback, null),
            finish: (s, result) => s.screenshot_finish(result),
        }, metadata);
    }

    captureRegion(handle, x, y, width, height, fdList) {
        if (width <= 0 || height <= 0)
            throw bridgeError('InvalidArgument', 'capture region must be non-empty');
        const metadata = captureRect(x, y, width, height);
        const fd = resolveFD(handle, fdList);
        return runScreenshot(fd, {
            begin: (s, output, callback) => s.screenshot_area(
                x, y, width, height, output, callback, null),
            finish: (s, result) => s.screenshot_area_finish(result),
        }, metadata);
    }
}

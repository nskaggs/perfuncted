import Gio from 'gi://Gio';
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

function runScreenshot(fd, start) {
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
                resolve();
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
        return runScreenshot(resolveFD(handle, fdList), {
            begin: (s, output, callback) => s.screenshot(false, output, callback, null),
            finish: (s, result) => s.screenshot_finish(result),
        });
    }

    captureRegion(handle, x, y, width, height, fdList) {
        if (width <= 0 || height <= 0)
            throw bridgeError('InvalidArgument', 'capture region must be non-empty');
        return runScreenshot(resolveFD(handle, fdList), {
            begin: (s, output, callback) => s.screenshot_area(
                x, y, width, height, output, callback, null),
            finish: (s, result) => s.screenshot_area_finish(result),
        });
    }
}

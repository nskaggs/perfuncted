import GLib from 'gi://GLib';
import Gio from 'gi://Gio';
import Shell from 'gi://Shell';

function runScreenshot(start) {
    const stream = start.stream;
    const screenshot = new Shell.Screenshot();
    let failure = null;
    const loop = GLib.MainLoop.new(null, false);
    start.begin(screenshot, stream, (_object, result) => {
        try {
            const outcome = start.finish(screenshot, result);
            if (outcome === false || (Array.isArray(outcome) && outcome[0] === false))
                failure = new Error('GNOME screenshot operation failed');
        } catch (error) {
            failure = error;
        } finally {
            loop.quit();
        }
    });
    loop.run();
    try {
        stream.close(null);
    } catch (error) {
        failure ??= error;
    }
    if (failure)
        throw failure;
}

export class Screen {
    captureFull(fd) {
        const stream = new Gio.UnixOutputStream({fd: Number(fd), close_fd: false});
        runScreenshot({
            stream,
            begin: (s, output, callback) => s.screenshot(false, output, callback, null),
            finish: (s, result) => s.screenshot_finish(result),
        });
    }

    captureRegion(fd, x, y, width, height) {
        if (width <= 0 || height <= 0)
            throw new Error('capture region must be non-empty');
        const stream = new Gio.UnixOutputStream({fd: Number(fd), close_fd: false});
        runScreenshot({
            stream,
            begin: (s, output, callback) => s.screenshot_area(
                x, y, width, height, output, callback, null),
            finish: (s, result) => s.screenshot_area_finish(result),
        });
    }
}

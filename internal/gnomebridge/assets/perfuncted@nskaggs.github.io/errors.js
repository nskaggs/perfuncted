const ERROR_PREFIX = 'io.github.nskaggs.perfuncted.Gnome1.Error.';

export function bridgeError(kind, message) {
    const error = new Error(message);
    error.name = ERROR_PREFIX + kind;
    return error;
}

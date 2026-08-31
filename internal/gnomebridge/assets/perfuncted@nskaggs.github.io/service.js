import GLib from 'gi://GLib';
import Gio from 'gi://Gio';

import * as Config from 'resource:///org/gnome/shell/misc/config.js';

import {Clipboard} from './clipboard.js';
import {Input} from './input.js';
import {Screen} from './screen.js';
import {Windows} from './windows.js';

const BUS_NAME = 'io.github.nskaggs.perfuncted.Gnome1';
const OBJECT_PATH = '/io/github/nskaggs/perfuncted/Gnome1';
const EXTENSION_VERSION = '1';
const PROTOCOL_VERSION = 1;

const CORE_XML = `
<node>
  <interface name="io.github.nskaggs.perfuncted.Gnome1.Core">
    <method name="GetProtocolVersion"><arg name="version" type="u" direction="out"/></method>
    <method name="GetExtensionVersion"><arg name="version" type="s" direction="out"/></method>
    <method name="GetShellVersion"><arg name="version" type="s" direction="out"/></method>
    <method name="GetCapabilities"><arg name="capabilities" type="as" direction="out"/></method>
    <method name="Ping"/>
  </interface>
</node>`;

const WINDOWS_XML = `
<node>
  <interface name="io.github.nskaggs.perfuncted.Gnome1.Windows">
    <method name="ListWindows"><arg name="windows" type="a(ssssiiiiibbbb)" direction="out"/></method>
    <method name="GetWindow"><arg name="id" type="s" direction="in"/><arg name="window" type="(ssssiiiiibbbb)" direction="out"/></method>
    <method name="GetActiveWindow"><arg name="window" type="(ssssiiiiibbbb)" direction="out"/></method>
    <method name="Activate"><arg name="id" type="s" direction="in"/></method>
    <method name="Move"><arg name="id" type="s" direction="in"/><arg name="x" type="i" direction="in"/><arg name="y" type="i" direction="in"/></method>
    <method name="Resize"><arg name="id" type="s" direction="in"/><arg name="width" type="i" direction="in"/><arg name="height" type="i" direction="in"/></method>
    <method name="Minimize"><arg name="id" type="s" direction="in"/></method>
    <method name="Maximize"><arg name="id" type="s" direction="in"/></method>
    <method name="Restore"><arg name="id" type="s" direction="in"/></method>
    <method name="Fullscreen"><arg name="id" type="s" direction="in"/></method>
    <method name="Unfullscreen"><arg name="id" type="s" direction="in"/></method>
    <method name="Close"><arg name="id" type="s" direction="in"/></method>
    <signal name="WindowAdded"><arg name="window" type="(ssssiiiiibbbb)"/></signal>
    <signal name="WindowRemoved"><arg name="id" type="s"/></signal>
    <signal name="WindowChanged"><arg name="window" type="(ssssiiiiibbbb)"/></signal>
    <signal name="FocusChanged"><arg name="id" type="s"/></signal>
  </interface>
</node>`;

const SCREEN_XML = `
<node>
  <interface name="io.github.nskaggs.perfuncted.Gnome1.Screen">
    <method name="CaptureFull"><arg name="fd" type="h" direction="in"/></method>
    <method name="CaptureRegion">
      <arg name="fd" type="h" direction="in"/><arg name="x" type="i" direction="in"/><arg name="y" type="i" direction="in"/>
      <arg name="width" type="i" direction="in"/><arg name="height" type="i" direction="in"/>
    </method>
  </interface>
</node>`;

const INPUT_XML = `
<node>
  <interface name="io.github.nskaggs.perfuncted.Gnome1.Input">
    <method name="Key"><arg name="keyval" type="u" direction="in"/><arg name="pressed" type="b" direction="in"/></method>
    <method name="Text"><arg name="text" type="s" direction="in"/></method>
    <method name="Paste"><arg name="text" type="s" direction="in"/></method>
    <method name="PointerMove"><arg name="x" type="i" direction="in"/><arg name="y" type="i" direction="in"/></method>
    <method name="PointerButton"><arg name="button" type="u" direction="in"/><arg name="pressed" type="b" direction="in"/></method>
    <method name="Scroll"><arg name="axis" type="s" direction="in"/><arg name="amount" type="d" direction="in"/></method>
    <method name="PointerLocation"><arg name="x" type="i" direction="out"/><arg name="y" type="i" direction="out"/></method>
  </interface>
</node>`;

const CLIPBOARD_XML = `
<node>
  <interface name="io.github.nskaggs.perfuncted.Gnome1.Clipboard">
    <method name="GetText"><arg name="text" type="s" direction="out"/></method>
    <method name="SetText"><arg name="text" type="s" direction="in"/></method>
  </interface>
</node>`;

const ERROR_PREFIX = `${BUS_NAME}.Error.`;

function bridgeError(kind, message) {
    const error = new Error(message);
    error.name = ERROR_PREFIX + kind;
    return error;
}

export class BridgeService {
    constructor() {
        this._objects = [];
        this._windowsObject = null;
        this._ownerId = 0;
        this._windows = new Windows((name, signature, value) => this._emit(name, signature, value));
        this._screen = null;
        this._input = null;
        this._clipboard = null;
        try {
            this._screen = new Screen();
        } catch (error) {
            console.warn(`perfuncted: GNOME screenshot unavailable: ${error}`);
        }
        try {
            this._input = new Input();
        } catch (error) {
            console.warn(`perfuncted: GNOME virtual input unavailable: ${error}`);
        }
        try {
            this._clipboard = new Clipboard();
        } catch (error) {
            console.warn(`perfuncted: GNOME clipboard unavailable: ${error}`);
        }
    }

    enable() {
        this._windows.enable();
        this._ownerId = Gio.bus_own_name(
            Gio.BusType.SESSION,
            BUS_NAME,
            Gio.BusNameOwnerFlags.NONE,
            (connection) => this._export(connection),
            () => {},
            () => this._unexport());
    }

    disable() {
        this._windows.disable();
        this._unexport();
        if (this._ownerId) {
            Gio.bus_unown_name(this._ownerId);
            this._ownerId = 0;
        }
    }

    _export(connection) {
        this._unexport();
        this._objects = [CORE_XML, WINDOWS_XML, SCREEN_XML, INPUT_XML, CLIPBOARD_XML]
            .map(xml => Gio.DBusExportedObject.wrapJSObject(xml, this));
        try {
            for (const object of this._objects)
                object.export(connection, OBJECT_PATH);
            this._windowsObject = this._objects[1];
        } catch (error) {
            this._unexport();
            throw error;
        }
    }

    _unexport() {
        for (const object of this._objects.splice(0)) {
            try {
                object.unexport();
            } catch (error) {
                console.warn(`perfuncted: failed to unexport GNOME bridge: ${error}`);
            }
        }
        this._windowsObject = null;
    }

    _emit(name, signature, value) {
        if (this._windowsObject)
            this._windowsObject.emit_signal(name, new GLib.Variant(signature, value));
    }

    _require(value, capability) {
        if (!value)
            throw bridgeError('Unsupported', `GNOME bridge capability unavailable: ${capability}`);
        return value;
    }

    GetProtocolVersion() { return PROTOCOL_VERSION; }
    GetExtensionVersion() { return EXTENSION_VERSION; }
    GetShellVersion() { return Config.PACKAGE_VERSION ?? ''; }
    GetCapabilities() {
        const capabilities = ['windows'];
        if (this._screen)
            capabilities.push('screen');
        if (this._input && this._clipboard)
            capabilities.push('input');
        if (this._clipboard)
            capabilities.push('clipboard');
        return capabilities;
    }
    Ping() {}

    ListWindows() { return this._require(this._windows, 'windows').list(); }
    GetWindow(id) { return this._require(this._windows, 'windows').get(id); }
    GetActiveWindow() { return this._require(this._windows, 'windows').active(); }
    Activate(id) { this._require(this._windows, 'windows').activate(id); }
    Move(id, x, y) { this._require(this._windows, 'windows').move(id, x, y); }
    Resize(id, width, height) { this._require(this._windows, 'windows').resize(id, width, height); }
    Minimize(id) { this._require(this._windows, 'windows').minimize(id); }
    Maximize(id) { this._require(this._windows, 'windows').maximize(id); }
    Restore(id) { this._require(this._windows, 'windows').restore(id); }
    Fullscreen(id) { this._require(this._windows, 'windows').fullscreen(id); }
    Unfullscreen(id) { this._require(this._windows, 'windows').unfullscreen(id); }
    Close(id) { this._require(this._windows, 'windows').close(id); }

    CaptureFull(fd, fdList) { return this._require(this._screen, 'screen').captureFull(fd, fdList); }
    CaptureRegion(fd, x, y, width, height, fdList) {
        return this._require(this._screen, 'screen').captureRegion(fd, x, y, width, height, fdList);
    }

    Key(keyval, pressed) { this._require(this._input, 'input').key(keyval, pressed); }
    Text(text) { return this._require(this._input, 'input').text(text); }
    Paste(text) {
        return this._require(this._input, 'input').pasteText(
            text, this._require(this._clipboard, 'clipboard'));
    }
    PointerMove(x, y) { this._require(this._input, 'input').pointerMove(x, y); }
    PointerButton(button, pressed) { this._require(this._input, 'input').pointerButton(button, pressed); }
    Scroll(axis, amount) { this._require(this._input, 'input').scroll(axis, amount); }
    PointerLocation() { return this._require(this._input, 'input').pointerLocation(); }

    GetText() { return this._require(this._clipboard, 'clipboard').getText(); }
    SetText(text) { this._require(this._clipboard, 'clipboard').setText(text); }
}

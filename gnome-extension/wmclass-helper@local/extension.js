import Gio from 'gi://Gio';
import GLib from 'gi://GLib';
import GObject from 'gi://GObject';

const BUS_NAME = 'org.gnome.Shell.WMClassHelper';
const OBJECT_PATH = '/org/gnome/Shell/WMClassHelper';

const INTERFACE_XML = `
<node>
  <interface name="org.gnome.Shell.WMClassHelper">
    <method name="GetFocusedWindowClass">
      <arg name="wm_class" type="s" direction="out"/>
    </method>
    <method name="GetFocusedWindowAppId">
      <arg name="app_id" type="s" direction="out"/>
    </method>
    <method name="GetFocusedWindowPID">
      <arg name="pid" type="i" direction="out"/>
    </method>
  </interface>
</node>`;

let _ownerId = 0;
let _dbusExported = null;
let _currentWmClass = '';
let _currentAppId = '';
let _currentPid = 0;
let _focusHandlerId = 0;

function _updateFocusedWindow() {
    const focusWindow = global.display.focus_window;
    if (!focusWindow) {
        _currentWmClass = '';
        _currentAppId = '';
        _currentPid = 0;
        return;
    }

    _currentWmClass = focusWindow.get_wm_class() || '';
    _currentAppId = focusWindow.get_sandboxed_app_id?.() ||
                    focusWindow.get_gtk_application_id?.() ||
                    _currentWmClass || '';
    _currentPid = focusWindow.get_pid?.() || 0;
}

export default class WMClassHelper {
    enable() {
        _updateFocusedWindow();

        _focusHandlerId = global.display.connect('notify::focus-window', () => {
            _updateFocusedWindow();
        });

        const impl = {
            GetFocusedWindowClass() {
                return _currentWmClass;
            },
            GetFocusedWindowAppId() {
                return _currentAppId;
            },
            GetFocusedWindowPID() {
                return _currentPid;
            }
        };

        _dbusExported = Gio.DBusExportedObject.wrapJSObject(INTERFACE_XML, impl);

        _ownerId = Gio.DBus.session.own_name(
            BUS_NAME,
            Gio.BusNameOwnerFlags.REPLACE,
            null,
            null,
            null
        );

        _dbusExported.export(Gio.DBus.session, OBJECT_PATH);
    }

    disable() {
        if (_focusHandlerId) {
            global.display.disconnect(_focusHandlerId);
            _focusHandlerId = 0;
        }

        if (_dbusExported) {
            _dbusExported.unexport();
            _dbusExported = null;
        }

        if (_ownerId) {
            Gio.DBus.session.unown_name(_ownerId);
            _ownerId = 0;
        }

        _currentWmClass = '';
        _currentAppId = '';
        _currentPid = 0;
    }
}

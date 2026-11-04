#!/usr/bin/env python3
"""Ask the Background portal to keep Frostread in the background.

This helper subscribes to the portal request before it makes the call.
It stays in a GLib main loop until the portal answers or 600 seconds pass.
It always exits 0 and writes an outcome to frostread/background.json.
"""

import json
import os
import sys
import tempfile
import time

TIMEOUT_SECONDS = 600
REASON = "Keep your feeds up to date when no window is open"
COMMANDLINE = ["frostread", "--background"]

PORTAL = "org.freedesktop.portal.Desktop"
PORTAL_PATH = "/org/freedesktop/portal/desktop"


def log(message):
    print(f"frostread-request-background: {message}", file=sys.stderr, flush=True)


def state_dir():
    base = os.environ.get("XDG_STATE_HOME")
    if not base:
        base = os.path.join(os.path.expanduser("~"), ".local", "state")
    return os.path.join(base, "frostread")


def write_outcome(outcome):
    directory = state_dir()
    path = os.path.join(directory, "background.json")
    temp = None
    try:
        os.makedirs(directory, exist_ok=True)
        fd, temp = tempfile.mkstemp(dir=directory, prefix=".background-", suffix=".tmp")
        with os.fdopen(fd, "w", encoding="utf-8") as handle:
            json.dump(outcome, handle)
            handle.write("\n")
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(temp, path)
        temp = None
    except OSError as err:
        log(f"cannot write {path}: {err}")
        return False
    finally:
        if temp is not None:
            try:
                os.unlink(temp)
            except OSError:
                pass
    return True


def fail(outcome, message):
    log(message)
    outcome["error"] = message


def flag(results, key):
    value = results.get(key)
    if value is None:
        return None
    unpack = getattr(value, "unpack", None)
    if unpack is not None:
        value = unpack()
    return bool(value)


def ask(outcome):
    try:
        from gi.repository import Gio, GLib
    except (ImportError, ValueError) as err:
        fail(outcome, f"no PyGObject: {err}")
        return

    try:
        bus = Gio.bus_get_sync(Gio.BusType.SESSION, None)
    except GLib.Error as err:
        fail(outcome, f"no session bus: {err.message}")
        return

    loop = GLib.MainLoop()
    answer = {}

    def on_response(_conn, _sender, _path, _iface, _signal, params, _data):
        code, results = params.unpack()
        answer["code"] = code
        answer["results"] = results
        loop.quit()

    def subscribe(request_path):
        return bus.signal_subscribe(
            PORTAL,
            "org.freedesktop.portal.Request",
            "Response",
            request_path,
            None,
            Gio.DBusSignalFlags.NONE,
            on_response,
            None,
        )

    token = f"frostread{os.getpid()}"
    sender = bus.get_unique_name().lstrip(":").replace(".", "_")
    predicted = f"{PORTAL_PATH}/request/{sender}/{token}"
    subscribe(predicted)

    options = {
        "handle_token": GLib.Variant("s", token),
        "reason": GLib.Variant("s", REASON),
        "autostart": GLib.Variant("b", True),
        "commandline": GLib.Variant("as", COMMANDLINE),
    }
    try:
        reply = bus.call_sync(
            PORTAL,
            PORTAL_PATH,
            "org.freedesktop.portal.Background",
            "RequestBackground",
            GLib.Variant("(sa{sv})", ("", options)),
            GLib.VariantType("(o)"),
            Gio.DBusCallFlags.NONE,
            -1,
            None,
        )
    except GLib.Error as err:
        fail(outcome, f"RequestBackground failed: {err.message}")
        return

    handle = reply.unpack()[0]
    if handle != predicted:
        log(f"the portal chose {handle}, not {predicted}; subscribing to it")
        subscribe(handle)

    def on_timeout():
        loop.quit()
        return False

    GLib.timeout_add_seconds(TIMEOUT_SECONDS, on_timeout)
    loop.run()

    if "code" not in answer:
        fail(outcome, f"no answer in {TIMEOUT_SECONDS} s")
        return
    results = dict(answer["results"])
    log(f"response {answer['code']} (0 is granted): {results}")
    outcome["response"] = int(answer["code"])
    outcome["background"] = flag(results, "background")
    outcome["autostart"] = flag(results, "autostart")


def main():
    outcome = {
        "at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        "response": None,
        "background": None,
        "autostart": None,
        "error": None,
    }
    try:
        ask(outcome)
    except Exception as err:
        fail(outcome, f"unexpected failure: {err}")
    write_outcome(outcome)
    return 0


if __name__ == "__main__":
    sys.exit(main())

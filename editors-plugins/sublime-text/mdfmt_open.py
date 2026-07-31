import os
import shutil
import subprocess

import sublime
import sublime_plugin


MDFMT_BINARY = "mdfmt"


def find_mdfmt():
    path = shutil.which(MDFMT_BINARY)
    if path:
        return path

    # GUI-launched Sublime often has a minimal PATH, so check the usual spots.
    for candidate in (
        os.path.expanduser("~/bin/mdfmt"),
        os.path.expanduser("~/.local/bin/mdfmt"),
        "/opt/homebrew/bin/mdfmt",
        "/usr/local/bin/mdfmt",
    ):
        if os.path.isfile(candidate) and os.access(candidate, os.X_OK):
            return candidate

    return None


class MdfmtOpenCommand(sublime_plugin.TextCommand):
    def run(self, edit):
        filename = self.view.file_name()
        if not filename:
            sublime.error_message("mdfmt: save the file before running mdfmt open.")
            return

        if self.view.is_dirty():
            self.view.run_command("save")

        mdfmt = find_mdfmt()
        if not mdfmt:
            sublime.error_message(
                "mdfmt: could not find the 'mdfmt' executable on PATH."
            )
            return

        try:
            proc = subprocess.Popen(
                [mdfmt, "open", filename],
                cwd=os.path.dirname(filename),
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
            )
        except OSError as err:
            sublime.error_message("mdfmt: failed to run '{}': {}".format(mdfmt, err))
            return

        sublime.set_timeout_async(lambda: self.report(proc, filename), 0)

    def report(self, proc, filename):
        output, _ = proc.communicate()
        message = (output or b"").decode("utf-8", "replace").strip()

        if proc.returncode == 0:
            sublime.status_message("mdfmt open: {}".format(os.path.basename(filename)))
        else:
            sublime.error_message(
                "mdfmt open failed (exit {}):\n\n{}".format(proc.returncode, message)
            )

    def is_enabled(self):
        return self.view.file_name() is not None

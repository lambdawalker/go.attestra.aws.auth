package workflow

import (
	"errors"
	"strings"
)

// AWS SDK v2 runs credential_process through cmd.exe on Windows. Go's
// standard argument escaping turns embedded quotes into literal backslash-
// quotes there. Use an absolute, unquoted shell-safe path instead.
func credentialExecutable(path, goos string, shortPath func(string) (string, error)) (string, error) {
	if goos == "windows" {
		safe := func(p string) bool {
			return p != "" && !strings.ContainsAny(p, " \t\r\n\"&|<>()^%!;'$\x60")
		}
		if !safe(path) {
			var err error
			path, err = shortPath(path)
			if err != nil || !safe(path) {
				return "", errors.New("cannot launch AWS credential helper safely: Windows short paths are unavailable for this temporary directory; set TEMP and TMP to an existing directory without spaces or shell characters (for example C:\\attestra-temp), update GOTMPDIR too if set, or move a prebuilt executable to such a directory, then rerun")
			}
		}
		return strings.ReplaceAll(path, "\\", "/"), nil
	}
	if strings.ContainsAny(path, "\"\r\n$\x60%!") {
		return "", errors.New("credential helper executable path contains unsupported shell characters; run from a standard temporary directory")
	}
	return "\"" + strings.ReplaceAll(path, "\\", "\\\\") + "\"", nil
}

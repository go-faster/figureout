package file_test

import (
	"os"
	"strings"
)

func writeFile(path, contents string) error {
	return os.WriteFile(path, []byte(contents), 0o600)
}

func upperSnake(segments []string) string {
	return strings.ToUpper(strings.Join(segments, "_"))
}

package nodes

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var portRangePattern = regexp.MustCompile(`^[0-9]{1,5}(-[0-9]{1,5})?(,[0-9]{1,5}(-[0-9]{1,5})?)*$`)

func ValidatePortRange(value string) error {
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		segs := strings.SplitN(part, "-", 2)
		start, err := strconv.Atoi(segs[0])
		if err != nil || start < 1 || start > 65535 {
			return fmt.Errorf("invalid port range %q", part)
		}
		if len(segs) == 2 {
			end, err := strconv.Atoi(segs[1])
			if err != nil || end < start || end > 65535 {
				return fmt.Errorf("invalid port range %q", part)
			}
		}
	}
	return nil
}

package hosts

import (
	"fmt"

	"github.com/meistro57/beechief/internal/config"
)

// ForHive builds the Execution for a configured Hive.
func ForHive(h config.Hive) (Execution, error) {
	if err := h.Validate(); err != nil {
		return nil, err
	}
	switch h.Kind() {
	case config.TypeLocal:
		return &LocalExecution{Hive: h.Name}, nil
	case config.TypeSSH:
		return &SSHExecution{Hive: h.Name, Host: h.Host, User: h.User, Port: h.Port}, nil
	default:
		return nil, fmt.Errorf("hive %q: unsupported type %q", h.Name, h.Kind())
	}
}

package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Database pool settings (PAC-81 AC-A9, spec D-6). Each is optional; a
// non-positive or unparseable value is ignored with a startup warning and the
// default is kept.
const (
	EnvDBMaxOpenConns    = "PACEDAY_DB_MAX_OPEN_CONNS"
	EnvDBMaxIdleConns    = "PACEDAY_DB_MAX_IDLE_CONNS"
	EnvDBConnMaxLifetime = "PACEDAY_DB_CONN_MAX_LIFETIME"
	EnvDBConnMaxIdleTime = "PACEDAY_DB_CONN_MAX_IDLE_TIME"
)

// DBPool is the decided pool.
type DBPool struct {
	MaxOpen     int
	MaxIdle     int
	MaxLifetime time.Duration
	MaxIdleTime time.Duration
}

// DefaultDBPool: 20 open, 10 idle, 30 min lifetime, 5 min idle time. Two
// backends during a deploy use 40 of Postgres's 97 usable connections.
var DefaultDBPool = DBPool{MaxOpen: 20, MaxIdle: 10, MaxLifetime: 30 * time.Minute, MaxIdleTime: 5 * time.Minute}

// DecideDBPool reads the pool settings through getenv. It never fails.
// The idle cap is lowered to the open cap when it is larger.
func DecideDBPool(getenv func(string) string) (DBPool, []string) {
	p := DefaultDBPool
	var notices []string
	readInt := func(name string, dst *int) {
		raw := strings.TrimSpace(getenv(name))
		if raw == "" {
			return
		}
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			notices = append(notices, fmt.Sprintf("%s=%q ignored: it must be a positive whole number; the default %d is kept", name, raw, *dst))
			return
		}
		*dst = n
	}
	readDur := func(name string, dst *time.Duration) {
		raw := strings.TrimSpace(getenv(name))
		if raw == "" {
			return
		}
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 {
			notices = append(notices, fmt.Sprintf("%s=%q ignored: it must be a positive duration such as 30m; the default %s is kept", name, raw, *dst))
			return
		}
		*dst = d
	}
	readInt(EnvDBMaxOpenConns, &p.MaxOpen)
	readInt(EnvDBMaxIdleConns, &p.MaxIdle)
	readDur(EnvDBConnMaxLifetime, &p.MaxLifetime)
	readDur(EnvDBConnMaxIdleTime, &p.MaxIdleTime)
	if p.MaxIdle > p.MaxOpen {
		p.MaxIdle = p.MaxOpen
	}
	return p, notices
}

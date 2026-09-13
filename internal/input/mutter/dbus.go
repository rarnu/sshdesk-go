package mutter

import (
	"context"
	"fmt"
	"time"

	"github.com/godbus/dbus/v5"
)

// callTimeout mirrors the Python one second D-Bus call timeout.
const callTimeout = time.Second

// NewDBusCaller wraps a godbus connection as the input caller for one
// remote desktop session.
func NewDBusCaller(conn *dbus.Conn, sessionPath dbus.ObjectPath) Caller {
	object := conn.Object(RemoteName, sessionPath)
	return func(method, signature string, values ...any) error {
		ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
		defer cancel()
		result := object.CallWithContext(ctx, SessionInterface+"."+method, 0, values...)
		if result.Err != nil {
			return fmt.Errorf("GNOME input failed: %v", result.Err)
		}
		return nil
	}
}

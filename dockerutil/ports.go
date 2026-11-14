package dockerutil

import (
	"fmt"
	"net"

	"github.com/docker/go-connections/nat"
)

type Listeners []net.Listener

func (l Listeners) CloseAll() {
	for _, listener := range l {
		listener.Close()
	}
}

// OpenListener opens a listener on a port. Set to 0 to get a random port.
func OpenListener(port int) (*net.TCPListener, error) {
	addr, err := net.ResolveTCPAddr("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, err
	}

	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return nil, err
	}

	return l, nil
}

// GetPort generates a docker PortBinding by using the port provided.
// If port is set to 0, the next available port will be used.
// The listener will be closed in the case of an error, otherwise it will be left open.
// This allows multiple GetPort calls to find multiple available ports
// before closing them so they are available for the PortBinding.
func GetPort(port int) (nat.PortBinding, *net.TCPListener, error) {
	l, err := OpenListener(port)
	if err != nil {
		if l != nil {
			_ = l.Close()
		}

		return nat.PortBinding{}, nil, err
	}

	return nat.PortBinding{
		HostIP:   "0.0.0.0",
		HostPort: fmt.Sprint(l.Addr().(*net.TCPAddr).Port),
	}, l, nil
}

// GeneratePortBindings requests Docker-assigned host ports for every container port.
// Existing bindings are ignored so Docker can allocate host ports without races.
// No listeners are opened; the extra return values are kept for compatibility.
func GeneratePortBindings(pairs nat.PortMap) (nat.PortMap, Listeners, error) {
	bindings := make(nat.PortMap, len(pairs))
	for port := range pairs {
		bindings[port] = []nat.PortBinding{
			{
				HostIP:   "0.0.0.0",
				HostPort: "",
			},
		}
	}

	return bindings, nil, nil
}

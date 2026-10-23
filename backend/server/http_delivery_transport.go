package server

import (
	"errors"

	"github.com/cloudwego/hertz/pkg/network"
	"github.com/cloudwego/hertz/pkg/network/standard"
)

// Embed the concrete standard connections so optional interfaces, including
// TLS and error normalization, survive the logging adapter. Unknown transports
// keep their own behavior and diagnostics.
type deliveryConn struct{ *standard.Conn }
type deliveryTLSConn struct{ *standard.TLSConn }

func observeHTTPConnection(conn interface{}) interface{} {
	switch conn := conn.(type) {
	case *standard.Conn:
		return &deliveryConn{conn}
	case *standard.TLSConn:
		return &deliveryTLSConn{conn}
	default:
		return conn
	}
}

func (conn *deliveryConn) Flush() error {
	return wrapDownstreamError(conn.Conn.Flush(), deliveryFlush)
}

func (conn *deliveryTLSConn) Flush() error {
	return wrapDownstreamError(conn.TLSConn.Flush(), deliveryFlush)
}

func (conn *deliveryConn) HandleSpecificError(err error, remote string) bool {
	return isReportedDeliveryError(err) || conn.Conn.HandleSpecificError(err, remote)
}

func (conn *deliveryTLSConn) HandleSpecificError(err error, remote string) bool {
	return isReportedDeliveryError(err) || conn.TLSConn.HandleSpecificError(err, remote)
}

func isReportedDeliveryError(err error) bool {
	var failure *downstreamWriteError
	return errors.As(err, &failure) && failure.isReported
}

var (
	_ network.Conn               = (*deliveryConn)(nil)
	_ network.ConnTLSer          = (*deliveryTLSConn)(nil)
	_ network.ErrorNormalization = (*deliveryConn)(nil)
	_ network.ErrorNormalization = (*deliveryTLSConn)(nil)
)

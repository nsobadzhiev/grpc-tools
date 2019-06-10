package grpc_proxy

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"github.com/bradleyjkemp/grpc-tools/internal"
	"github.com/improbable-eng/grpc-web/go/grpcweb"
	"google.golang.org/grpc"
	"net"
	"os"
)

type server struct {
	serverOptions []grpc.ServerOption
	grpcServer    *grpc.Server

	port     int
	certFile string
	keyFile  string
	x509Cert *x509.Certificate
	tlsCert  tls.Certificate

	destination string
	connPool    *internal.ConnPool

	listener net.Listener
}

func New(configurators ...Configurator) (*server, error) {
	s := &server{
		connPool: internal.NewConnPool(),
	}
	s.serverOptions = []grpc.ServerOption{
		grpc.CustomCodec(NoopCodec{}),              // Allows for passing raw []byte messages around
		grpc.UnknownServiceHandler(s.proxyHandler), // All services are unknown so will be proxied
	}

	for _, configurator := range configurators {
		configurator(s)
	}

	if s.certFile != "" && s.keyFile != "" {
		var err error
		s.tlsCert, err = tls.LoadX509KeyPair(s.certFile, s.keyFile)
		if err != nil {
			return nil, err
		}

		s.x509Cert, err = x509.ParseCertificate(s.tlsCert.Certificate[0]) //TODO do we need to parse anything other than [0]?
		if err != nil {
			return nil, err
		}
	}

	return s, nil
}

func (s *server) Start() error {
	listener, err := net.Listen("tcp", fmt.Sprintf("localhost:%d", s.port))
	if err != nil {
		return fmt.Errorf("failed to listen on port (%d): %v", s.port, err)
	}
	fmt.Fprintf(os.Stderr, "Listening on %s\n", listener.Addr()) // TODO move this to a logger controllable by options

	grpcWebHandler := grpcweb.WrapServer(
		grpc.NewServer(s.serverOptions...),
		grpcweb.WithCorsForRegisteredEndpointsOnly(false), // because we are proxying
	)

	proxyLis := newProxyListener(listener.(*net.TCPListener))

	httpServer := newHttpServer(grpcWebHandler, proxyLis.internalRedirect)
	httpsServer := withHttpsMiddleware(newHttpServer(grpcWebHandler, proxyLis.internalRedirect))

	httpLis, httpsLis := newTlsMux(proxyLis, s.x509Cert, s.tlsCert)

	// the TLSMux unwraps TLS for us so we use Serve instead of ServeTLS
	go httpsServer.Serve(httpsLis)
	return httpServer.Serve(httpLis)
}

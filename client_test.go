package quic

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/lucas-clemente/quic-go/internal/handshake"
	"github.com/lucas-clemente/quic-go/internal/protocol"
	"github.com/lucas-clemente/quic-go/internal/utils"
	"github.com/lucas-clemente/quic-go/internal/wire"
	"github.com/lucas-clemente/quic-go/qerr"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Client", func() {
	var (
		cl              *client
		packetConn      *mockPacketConn
		addr            net.Addr
		connID          protocol.ConnectionID
		mockMultiplexer *MockMultiplexer
		origMultiplexer multiplexer

		originalClientSessConstructor func(connection, sessionRunner, string, protocol.VersionNumber, protocol.ConnectionID, *tls.Config, *Config, protocol.VersionNumber, []protocol.VersionNumber, utils.Logger) (quicSession, error)
	)

	// generate a packet sent by the server that accepts the QUIC version suggested by the client
	acceptClientVersionPacket := func(connID protocol.ConnectionID) []byte {
		b := &bytes.Buffer{}
		err := (&wire.Header{
			DestConnectionID: connID,
			PacketNumber:     1,
			PacketNumberLen:  1,
		}).Write(b, protocol.PerspectiveServer, protocol.VersionWhatever)
		Expect(err).ToNot(HaveOccurred())
		return b.Bytes()
	}

	composeVersionNegotiationPacket := func(connID protocol.ConnectionID, versions []protocol.VersionNumber) *receivedPacket {
		return &receivedPacket{
			rcvTime: time.Now(),
			header: &wire.Header{
				IsVersionNegotiation: true,
				DestConnectionID:     connID,
				SupportedVersions:    versions,
			},
		}
	}

	BeforeEach(func() {
		connID = protocol.ConnectionID{0, 0, 0, 0, 0, 0, 0x13, 0x37}
		originalClientSessConstructor = newClientSession
		Eventually(areSessionsRunning).Should(BeFalse())
		// sess = NewMockQuicSession(mockCtrl)
		addr = &net.UDPAddr{IP: net.IPv4(192, 168, 100, 200), Port: 1337}
		packetConn = newMockPacketConn()
		packetConn.addr = &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1234}
		packetConn.dataReadFrom = addr
		cl = &client{
			srcConnID:  connID,
			destConnID: connID,
			version:    protocol.SupportedVersions[0],
			conn:       &conn{pconn: packetConn, currentAddr: addr},
			logger:     utils.DefaultLogger,
		}
		getClientMultiplexer() // make the sync.Once execute
		// replace the clientMuxer. getClientMultiplexer will now return the MockMultiplexer
		mockMultiplexer = NewMockMultiplexer(mockCtrl)
		origMultiplexer = clientMuxer
		clientMuxer = mockMultiplexer
	})

	AfterEach(func() {
		clientMuxer = origMultiplexer
		newClientSession = originalClientSessConstructor
	})

	AfterEach(func() {
		if s, ok := cl.session.(*session); ok {
			s.Close()
		}
		Eventually(areSessionsRunning).Should(BeFalse())
	})

	Context("Dialing", func() {
		var origGenerateConnectionID func(int) (protocol.ConnectionID, error)

		BeforeEach(func() {
			origGenerateConnectionID = generateConnectionID
			generateConnectionID = func(int) (protocol.ConnectionID, error) {
				return connID, nil
			}
		})

		AfterEach(func() {
			generateConnectionID = origGenerateConnectionID
		})

		It("resolves the address", func() {
			manager := NewMockPacketHandlerManager(mockCtrl)
			mockMultiplexer.EXPECT().AddConn(gomock.Any(), gomock.Any()).Return(manager, nil)
			mockMultiplexer.EXPECT().AddHandler(gomock.Any(), gomock.Any(), gomock.Any())

			if os.Getenv("APPVEYOR") == "True" {
				Skip("This test is flaky on AppVeyor.")
			}
			remoteAddrChan := make(chan string, 1)
			newClientSession = func(
				conn connection,
				_ sessionRunner,
				_ string,
				_ protocol.VersionNumber,
				_ protocol.ConnectionID,
				_ *tls.Config,
				_ *Config,
				_ protocol.VersionNumber,
				_ []protocol.VersionNumber,
				_ utils.Logger,
			) (quicSession, error) {
				remoteAddrChan <- conn.RemoteAddr().String()
				sess := NewMockQuicSession(mockCtrl)
				sess.EXPECT().run()
				return sess, nil
			}
			_, err := DialAddr("localhost:17890", nil, &Config{HandshakeTimeout: time.Millisecond})
			Expect(err).ToNot(HaveOccurred())
			Eventually(remoteAddrChan).Should(Receive(Equal("127.0.0.1:17890")))
		})

		It("uses the tls.Config.ServerName as the hostname, if present", func() {
			manager := NewMockPacketHandlerManager(mockCtrl)
			mockMultiplexer.EXPECT().AddConn(gomock.Any(), gomock.Any()).Return(manager, nil)
			mockMultiplexer.EXPECT().AddHandler(gomock.Any(), gomock.Any(), gomock.Any())

			hostnameChan := make(chan string, 1)
			newClientSession = func(
				_ connection,
				_ sessionRunner,
				h string,
				_ protocol.VersionNumber,
				_ protocol.ConnectionID,
				_ *tls.Config,
				_ *Config,
				_ protocol.VersionNumber,
				_ []protocol.VersionNumber,
				_ utils.Logger,
			) (quicSession, error) {
				hostnameChan <- h
				sess := NewMockQuicSession(mockCtrl)
				sess.EXPECT().run()
				return sess, nil
			}
			_, err := DialAddr("localhost:17890", &tls.Config{ServerName: "foobar"}, nil)
			Expect(err).ToNot(HaveOccurred())
			Eventually(hostnameChan).Should(Receive(Equal("foobar")))
		})

		It("returns after the handshake is complete", func() {
			manager := NewMockPacketHandlerManager(mockCtrl)
			mockMultiplexer.EXPECT().AddConn(packetConn, gomock.Any()).Return(manager, nil)
			mockMultiplexer.EXPECT().AddHandler(packetConn, gomock.Any(), gomock.Any())

			run := make(chan struct{})
			newClientSession = func(
				_ connection,
				runner sessionRunner,
				_ string,
				_ protocol.VersionNumber,
				_ protocol.ConnectionID,
				_ *tls.Config,
				_ *Config,
				_ protocol.VersionNumber,
				_ []protocol.VersionNumber,
				_ utils.Logger,
			) (quicSession, error) {
				sess := NewMockQuicSession(mockCtrl)
				sess.EXPECT().run().Do(func() { close(run) })
				runner.onHandshakeComplete(sess)
				return sess, nil
			}
			s, err := Dial(packetConn, addr, "quic.clemente.io:1337", nil, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(s).ToNot(BeNil())
			Eventually(run).Should(BeClosed())
		})

		It("returns an error that occurs while waiting for the connection to become secure", func() {
			manager := NewMockPacketHandlerManager(mockCtrl)
			mockMultiplexer.EXPECT().AddConn(packetConn, gomock.Any()).Return(manager, nil)
			mockMultiplexer.EXPECT().AddHandler(packetConn, gomock.Any(), gomock.Any())

			testErr := errors.New("early handshake error")
			newClientSession = func(
				conn connection,
				_ sessionRunner,
				_ string,
				_ protocol.VersionNumber,
				_ protocol.ConnectionID,
				_ *tls.Config,
				_ *Config,
				_ protocol.VersionNumber,
				_ []protocol.VersionNumber,
				_ utils.Logger,
			) (quicSession, error) {
				sess := NewMockQuicSession(mockCtrl)
				sess.EXPECT().run().Return(testErr)
				return sess, nil
			}
			packetConn.dataToRead <- acceptClientVersionPacket(cl.srcConnID)
			_, err := Dial(packetConn, addr, "quic.clemente.io:1337", nil, nil)
			Expect(err).To(MatchError(testErr))
		})

		It("closes the session when the context is canceled", func() {
			manager := NewMockPacketHandlerManager(mockCtrl)
			mockMultiplexer.EXPECT().AddConn(packetConn, gomock.Any()).Return(manager, nil)
			mockMultiplexer.EXPECT().AddHandler(packetConn, gomock.Any(), gomock.Any())

			sessionRunning := make(chan struct{})
			defer close(sessionRunning)
			sess := NewMockQuicSession(mockCtrl)
			sess.EXPECT().run().Do(func() {
				<-sessionRunning
			})
			newClientSession = func(
				conn connection,
				_ sessionRunner,
				_ string,
				_ protocol.VersionNumber,
				_ protocol.ConnectionID,
				_ *tls.Config,
				_ *Config,
				_ protocol.VersionNumber,
				_ []protocol.VersionNumber,
				_ utils.Logger,
			) (quicSession, error) {
				return sess, nil
			}
			ctx, cancel := context.WithCancel(context.Background())
			dialed := make(chan struct{})
			go func() {
				defer GinkgoRecover()
				_, err := DialContext(ctx, packetConn, addr, "quic.clemnte.io:1337", nil, nil)
				Expect(err).To(MatchError(context.Canceled))
				close(dialed)
			}()
			Consistently(dialed).ShouldNot(BeClosed())
			sess.EXPECT().Close()
			cancel()
			Eventually(dialed).Should(BeClosed())
		})

		It("removes closed sessions from the multiplexer", func() {
			manager := NewMockPacketHandlerManager(mockCtrl)
			manager.EXPECT().Remove(connID)
			mockMultiplexer.EXPECT().AddConn(packetConn, gomock.Any()).Return(manager, nil)
			mockMultiplexer.EXPECT().AddHandler(packetConn, gomock.Any(), gomock.Any())

			var runner sessionRunner
			sess := NewMockQuicSession(mockCtrl)
			newClientSession = func(
				conn connection,
				runnerP sessionRunner,
				_ string,
				_ protocol.VersionNumber,
				connID protocol.ConnectionID,
				_ *tls.Config,
				_ *Config,
				_ protocol.VersionNumber,
				_ []protocol.VersionNumber,
				_ utils.Logger,
			) (quicSession, error) {
				runner = runnerP
				return sess, nil
			}
			sess.EXPECT().run().Do(func() {
				runner.removeConnectionID(connID)
			})

			_, err := DialContext(context.Background(), packetConn, addr, "quic.clemnte.io:1337", nil, nil)
			Expect(err).ToNot(HaveOccurred())
		})

		It("closes the connection when it was created by DialAddr", func() {
			manager := NewMockPacketHandlerManager(mockCtrl)
			mockMultiplexer.EXPECT().AddConn(gomock.Any(), gomock.Any()).Return(manager, nil)
			mockMultiplexer.EXPECT().AddHandler(gomock.Any(), gomock.Any(), gomock.Any())

			var conn connection
			run := make(chan struct{})
			sessionCreated := make(chan struct{})
			sess := NewMockQuicSession(mockCtrl)
			newClientSession = func(
				connP connection,
				_ sessionRunner,
				_ string,
				_ protocol.VersionNumber,
				connID protocol.ConnectionID,
				_ *tls.Config,
				_ *Config,
				_ protocol.VersionNumber,
				_ []protocol.VersionNumber,
				_ utils.Logger,
			) (quicSession, error) {
				conn = connP
				close(sessionCreated)
				return sess, nil
			}
			sess.EXPECT().run().Do(func() {
				<-run
			})

			done := make(chan struct{})
			go func() {
				defer GinkgoRecover()
				_, err := DialAddr("quic.clemente.io:1337", nil, nil)
				Expect(err).ToNot(HaveOccurred())
				close(done)
			}()

			Eventually(sessionCreated).Should(BeClosed())

			// check that the connection is not closed
			Expect(conn.Write([]byte("foobar"))).To(Succeed())

			close(run)
			time.Sleep(50 * time.Millisecond)
			// check that the connection is closed
			err := conn.Write([]byte("foobar"))
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("use of closed network connection"))

			Eventually(done).Should(BeClosed())
		})

		Context("quic.Config", func() {
			It("setups with the right values", func() {
				config := &Config{
					HandshakeTimeout:            1337 * time.Minute,
					IdleTimeout:                 42 * time.Hour,
					RequestConnectionIDOmission: true,
					MaxIncomingStreams:          1234,
					MaxIncomingUniStreams:       4321,
					ConnectionIDLength:          13,
				}
				c := populateClientConfig(config, false)
				Expect(c.HandshakeTimeout).To(Equal(1337 * time.Minute))
				Expect(c.IdleTimeout).To(Equal(42 * time.Hour))
				Expect(c.RequestConnectionIDOmission).To(BeTrue())
				Expect(c.MaxIncomingStreams).To(Equal(1234))
				Expect(c.MaxIncomingUniStreams).To(Equal(4321))
				Expect(c.ConnectionIDLength).To(Equal(13))
			})

			It("errors when the Config contains an invalid version", func() {
				manager := NewMockPacketHandlerManager(mockCtrl)
				mockMultiplexer.EXPECT().AddConn(packetConn, gomock.Any()).Return(manager, nil)

				version := protocol.VersionNumber(0x1234)
				_, err := Dial(packetConn, nil, "localhost:1234", &tls.Config{}, &Config{Versions: []protocol.VersionNumber{version}})
				Expect(err).To(MatchError("0x1234 is not a valid QUIC version"))
			})

			It("disables bidirectional streams", func() {
				config := &Config{
					MaxIncomingStreams:    -1,
					MaxIncomingUniStreams: 4321,
				}
				c := populateClientConfig(config, false)
				Expect(c.MaxIncomingStreams).To(BeZero())
				Expect(c.MaxIncomingUniStreams).To(Equal(4321))
			})

			It("disables unidirectional streams", func() {
				config := &Config{
					MaxIncomingStreams:    1234,
					MaxIncomingUniStreams: -1,
				}
				c := populateClientConfig(config, false)
				Expect(c.MaxIncomingStreams).To(Equal(1234))
				Expect(c.MaxIncomingUniStreams).To(BeZero())
			})

			It("uses 0-byte connection IDs when dialing an address", func() {
				config := &Config{}
				c := populateClientConfig(config, true)
				Expect(c.ConnectionIDLength).To(BeZero())
			})

			It("doesn't use 0-byte connection IDs when dialing an address", func() {
				config := &Config{}
				c := populateClientConfig(config, false)
				Expect(c.ConnectionIDLength).To(Equal(protocol.DefaultConnectionIDLength))
			})

			It("fills in default values if options are not set in the Config", func() {
				c := populateClientConfig(&Config{}, false)
				Expect(c.Versions).To(Equal(protocol.SupportedVersions))
				Expect(c.HandshakeTimeout).To(Equal(protocol.DefaultHandshakeTimeout))
				Expect(c.IdleTimeout).To(Equal(protocol.DefaultIdleTimeout))
				Expect(c.RequestConnectionIDOmission).To(BeFalse())
			})
		})

		Context("gQUIC", func() {
			It("errors if it can't create a session", func() {
				manager := NewMockPacketHandlerManager(mockCtrl)
				mockMultiplexer.EXPECT().AddConn(packetConn, gomock.Any()).Return(manager, nil)
				mockMultiplexer.EXPECT().AddHandler(packetConn, gomock.Any(), gomock.Any())

				testErr := errors.New("error creating session")
				newClientSession = func(
					_ connection,
					_ sessionRunner,
					_ string,
					_ protocol.VersionNumber,
					_ protocol.ConnectionID,
					_ *tls.Config,
					_ *Config,
					_ protocol.VersionNumber,
					_ []protocol.VersionNumber,
					_ utils.Logger,
				) (quicSession, error) {
					return nil, testErr
				}
				_, err := Dial(packetConn, addr, "quic.clemente.io:1337", nil, nil)
				Expect(err).To(MatchError(testErr))
			})
		})

		Context("IETF QUIC", func() {
			It("creates new TLS sessions with the right parameters", func() {
				manager := NewMockPacketHandlerManager(mockCtrl)
				mockMultiplexer.EXPECT().AddConn(packetConn, gomock.Any()).Return(manager, nil)
				mockMultiplexer.EXPECT().AddHandler(packetConn, gomock.Any(), gomock.Any())

				config := &Config{Versions: []protocol.VersionNumber{protocol.VersionTLS}}
				c := make(chan struct{})
				var cconn connection
				var hostname string
				var version protocol.VersionNumber
				var conf *Config
				newTLSClientSession = func(
					connP connection,
					_ sessionRunner,
					hostnameP string,
					versionP protocol.VersionNumber,
					_ protocol.ConnectionID,
					_ protocol.ConnectionID,
					configP *Config,
					tls handshake.MintTLS,
					paramsChan <-chan handshake.TransportParameters,
					_ protocol.PacketNumber,
					_ utils.Logger,
				) (quicSession, error) {
					cconn = connP
					hostname = hostnameP
					version = versionP
					conf = configP
					close(c)
					// TODO: check connection IDs?
					sess := NewMockQuicSession(mockCtrl)
					sess.EXPECT().run()
					return sess, nil
				}
				_, err := Dial(packetConn, addr, "quic.clemente.io:1337", nil, config)
				Expect(err).ToNot(HaveOccurred())
				Eventually(c).Should(BeClosed())
				Expect(cconn.(*conn).pconn).To(Equal(packetConn))
				Expect(hostname).To(Equal("quic.clemente.io"))
				Expect(version).To(Equal(config.Versions[0]))
				Expect(conf.Versions).To(Equal(config.Versions))
			})
		})

		Context("version negotiation", func() {
			var origSupportedVersions []protocol.VersionNumber

			BeforeEach(func() {
				origSupportedVersions = protocol.SupportedVersions
				protocol.SupportedVersions = append(protocol.SupportedVersions, []protocol.VersionNumber{77, 78}...)
			})

			AfterEach(func() {
				protocol.SupportedVersions = origSupportedVersions
			})

			It("returns an error that occurs during version negotiation", func() {
				manager := NewMockPacketHandlerManager(mockCtrl)
				mockMultiplexer.EXPECT().AddConn(packetConn, gomock.Any()).Return(manager, nil)
				mockMultiplexer.EXPECT().AddHandler(packetConn, gomock.Any(), gomock.Any())

				testErr := errors.New("early handshake error")
				newClientSession = func(
					conn connection,
					_ sessionRunner,
					_ string,
					_ protocol.VersionNumber,
					_ protocol.ConnectionID,
					_ *tls.Config,
					_ *Config,
					_ protocol.VersionNumber,
					_ []protocol.VersionNumber,
					_ utils.Logger,
				) (quicSession, error) {
					Expect(conn.Write([]byte("0 fake CHLO"))).To(Succeed())
					sess := NewMockQuicSession(mockCtrl)
					sess.EXPECT().run().Return(testErr)
					return sess, nil
				}
				_, err := Dial(packetConn, addr, "quic.clemente.io:1337", nil, nil)
				Expect(err).To(MatchError(testErr))
			})

			It("recognizes that a packet without VersionFlag means that the server accepted the suggested version", func() {
				sess := NewMockQuicSession(mockCtrl)
				sess.EXPECT().handlePacket(gomock.Any())
				cl.session = sess
				ph := &wire.Header{
					PacketNumber:     1,
					PacketNumberLen:  protocol.PacketNumberLen2,
					DestConnectionID: connID,
					SrcConnectionID:  connID,
				}
				err := cl.handlePacketImpl(&receivedPacket{header: ph})
				Expect(err).ToNot(HaveOccurred())
				Expect(cl.versionNegotiated).To(BeTrue())
			})

			It("changes the version after receiving a version negotiation packet", func() {
				mockMultiplexer.EXPECT().AddHandler(gomock.Any(), gomock.Any(), gomock.Any())

				version1 := protocol.Version39
				version2 := protocol.Version39 + 1
				Expect(version2.UsesTLS()).To(BeFalse())
				sess1 := NewMockQuicSession(mockCtrl)
				run1 := make(chan struct{})
				sess1.EXPECT().run().Do(func() { <-run1 }).Return(errCloseSessionForNewVersion)
				sess1.EXPECT().destroy(errCloseSessionForNewVersion).Do(func(error) { close(run1) })
				sess2 := NewMockQuicSession(mockCtrl)
				sess2.EXPECT().run()
				sessionChan := make(chan *MockQuicSession, 2)
				sessionChan <- sess1
				sessionChan <- sess2
				newClientSession = func(
					_ connection,
					_ sessionRunner,
					_ string,
					_ protocol.VersionNumber,
					connectionID protocol.ConnectionID,
					_ *tls.Config,
					_ *Config,
					_ protocol.VersionNumber,
					_ []protocol.VersionNumber,
					_ utils.Logger,
				) (quicSession, error) {
					return <-sessionChan, nil
				}

				cl.config = &Config{Versions: []protocol.VersionNumber{version1, version2}}
				dialed := make(chan struct{})
				go func() {
					defer GinkgoRecover()
					err := cl.dial(context.Background())
					Expect(err).ToNot(HaveOccurred())
					close(dialed)
				}()
				Eventually(sessionChan).Should(HaveLen(1))
				cl.handlePacket(composeVersionNegotiationPacket(connID, []protocol.VersionNumber{version2}))
				Eventually(sessionChan).Should(BeEmpty())
			})

			It("only accepts one version negotiation packet", func() {
				mockMultiplexer.EXPECT().AddHandler(gomock.Any(), gomock.Any(), gomock.Any())

				version1 := protocol.Version39
				version2 := protocol.Version39 + 1
				version3 := protocol.Version39 + 2
				Expect(version2.UsesTLS()).To(BeFalse())
				Expect(version3.UsesTLS()).To(BeFalse())
				sess1 := NewMockQuicSession(mockCtrl)
				run1 := make(chan struct{})
				sess1.EXPECT().run().Do(func() { <-run1 }).Return(errCloseSessionForNewVersion)
				sess1.EXPECT().destroy(errCloseSessionForNewVersion).Do(func(error) { close(run1) })
				sess2 := NewMockQuicSession(mockCtrl)
				sess2.EXPECT().run()
				sessionChan := make(chan *MockQuicSession, 2)
				sessionChan <- sess1
				sessionChan <- sess2
				newClientSession = func(
					_ connection,
					_ sessionRunner,
					_ string,
					_ protocol.VersionNumber,
					connectionID protocol.ConnectionID,
					_ *tls.Config,
					_ *Config,
					_ protocol.VersionNumber,
					_ []protocol.VersionNumber,
					_ utils.Logger,
				) (quicSession, error) {
					return <-sessionChan, nil
				}

				cl.config = &Config{Versions: []protocol.VersionNumber{version1, version2, version3}}
				dialed := make(chan struct{})
				go func() {
					defer GinkgoRecover()
					err := cl.dial(context.Background())
					Expect(err).ToNot(HaveOccurred())
					close(dialed)
				}()
				Eventually(sessionChan).Should(HaveLen(1))
				cl.handlePacket(composeVersionNegotiationPacket(connID, []protocol.VersionNumber{version2}))
				Eventually(sessionChan).Should(BeEmpty())
				Expect(cl.version).To(Equal(version2))
				cl.handlePacket(composeVersionNegotiationPacket(connID, []protocol.VersionNumber{version3}))
				Eventually(dialed).Should(BeClosed())
				Expect(cl.version).To(Equal(version2))
			})

			It("errors if no matching version is found", func() {
				sess := NewMockQuicSession(mockCtrl)
				sess.EXPECT().destroy(qerr.InvalidVersion)
				cl.session = sess
				cl.config = &Config{Versions: protocol.SupportedVersions}
				cl.handlePacket(composeVersionNegotiationPacket(connID, []protocol.VersionNumber{1}))
			})

			It("errors if the version is supported by quic-go, but disabled by the quic.Config", func() {
				sess := NewMockQuicSession(mockCtrl)
				sess.EXPECT().destroy(qerr.InvalidVersion)
				cl.session = sess
				v := protocol.VersionNumber(1234)
				Expect(v).ToNot(Equal(cl.version))
				cl.config = &Config{Versions: protocol.SupportedVersions}
				cl.handlePacket(composeVersionNegotiationPacket(connID, []protocol.VersionNumber{v}))
			})

			It("changes to the version preferred by the quic.Config", func() {
				mockMultiplexer.EXPECT().AddHandler(gomock.Any(), gomock.Any(), gomock.Any())
				sess := NewMockQuicSession(mockCtrl)
				sess.EXPECT().destroy(errCloseSessionForNewVersion)
				cl.session = sess
				versions := []protocol.VersionNumber{1234, 4321}
				cl.config = &Config{Versions: versions}
				cl.handlePacket(composeVersionNegotiationPacket(connID, versions))
				Expect(cl.version).To(Equal(protocol.VersionNumber(1234)))
			})

			It("drops version negotiation packets that contain the offered version", func() {
				cl.config = &Config{}
				ver := cl.version
				cl.handlePacket(composeVersionNegotiationPacket(connID, []protocol.VersionNumber{ver}))
				Expect(cl.version).To(Equal(ver))
			})
		})
	})

	It("tells its version", func() {
		Expect(cl.version).ToNot(BeZero())
		Expect(cl.GetVersion()).To(Equal(cl.version))
	})

	It("errors on packets that are smaller than the Payload Length in the packet header", func() {
		cl.session = NewMockQuicSession(mockCtrl) // don't EXPECT any handlePacket calls
		hdr := &wire.Header{
			IsLongHeader:     true,
			Type:             protocol.PacketTypeHandshake,
			PayloadLen:       1000,
			SrcConnectionID:  protocol.ConnectionID{1, 2, 3, 4, 5, 6, 7, 8},
			DestConnectionID: protocol.ConnectionID{1, 2, 3, 4, 5, 6, 7, 8},
			PacketNumberLen:  protocol.PacketNumberLen1,
			Version:          versionIETFFrames,
		}
		err := cl.handlePacketImpl(&receivedPacket{
			remoteAddr: addr,
			header:     hdr,
			data:       make([]byte, 456),
		})
		Expect(err).To(MatchError("received a packet with an unexpected connection ID (0x0102030405060708, expected 0x0000000000001337)"))
	})

	It("cuts packets at the payload length", func() {
		sess := NewMockQuicSession(mockCtrl)
		sess.EXPECT().handlePacket(gomock.Any()).Do(func(packet *receivedPacket) {
			Expect(packet.data).To(HaveLen(123))
		})
		cl.session = sess
		hdr := &wire.Header{
			IsLongHeader:     true,
			Type:             protocol.PacketTypeHandshake,
			PayloadLen:       123,
			SrcConnectionID:  connID,
			DestConnectionID: connID,
			PacketNumberLen:  protocol.PacketNumberLen1,
			Version:          versionIETFFrames,
		}
		err := cl.handlePacketImpl(&receivedPacket{
			remoteAddr: addr,
			header:     hdr,
			data:       make([]byte, 456),
		})
		Expect(err).ToNot(HaveOccurred())
	})

	It("ignores packets with the wrong Long Header Type", func() {
		hdr := &wire.Header{
			IsLongHeader:     true,
			Type:             protocol.PacketTypeInitial,
			PayloadLen:       123,
			SrcConnectionID:  connID,
			DestConnectionID: connID,
			PacketNumberLen:  protocol.PacketNumberLen1,
			Version:          versionIETFFrames,
		}
		err := cl.handlePacketImpl(&receivedPacket{
			remoteAddr: addr,
			header:     hdr,
			data:       make([]byte, 456),
		})
		Expect(err).To(MatchError("Received unsupported packet type: Initial"))
	})

	It("ignores packets without connection id, if it didn't request connection id trunctation", func() {
		cl.session = NewMockQuicSession(mockCtrl) // don't EXPECT any handlePacket calls
		cl.config = &Config{RequestConnectionIDOmission: false}
		hdr := &wire.Header{
			IsPublicHeader:  true,
			PacketNumber:    1,
			PacketNumberLen: 1,
		}
		err := cl.handlePacketImpl(&receivedPacket{
			remoteAddr: addr,
			header:     hdr,
		})
		Expect(err).To(MatchError("received packet with truncated connection ID, but didn't request truncation"))
	})

	It("ignores packets with the wrong destination connection ID", func() {
		cl.session = NewMockQuicSession(mockCtrl) // don't EXPECT any handlePacket calls
		cl.version = versionIETFFrames
		cl.config = &Config{RequestConnectionIDOmission: false}
		connID2 := protocol.ConnectionID{8, 7, 6, 5, 4, 3, 2, 1}
		Expect(connID).ToNot(Equal(connID2))
		hdr := &wire.Header{
			DestConnectionID: connID2,
			SrcConnectionID:  connID,
			PacketNumber:     1,
			PacketNumberLen:  protocol.PacketNumberLen1,
			Version:          versionIETFFrames,
		}
		err := cl.handlePacketImpl(&receivedPacket{
			remoteAddr: addr,
			header:     hdr,
		})
		Expect(err).To(MatchError(fmt.Sprintf("received a packet with an unexpected connection ID (0x0807060504030201, expected %s)", connID)))
	})

	It("creates new gQUIC sessions with the right parameters", func() {
		manager := NewMockPacketHandlerManager(mockCtrl)
		mockMultiplexer.EXPECT().AddConn(packetConn, gomock.Any()).Return(manager, nil)
		mockMultiplexer.EXPECT().AddHandler(packetConn, gomock.Any(), gomock.Any())

		config := &Config{Versions: protocol.SupportedVersions}
		c := make(chan struct{})
		var cconn connection
		var hostname string
		var version protocol.VersionNumber
		var conf *Config
		newClientSession = func(
			connP connection,
			_ sessionRunner,
			hostnameP string,
			versionP protocol.VersionNumber,
			_ protocol.ConnectionID,
			_ *tls.Config,
			configP *Config,
			_ protocol.VersionNumber,
			_ []protocol.VersionNumber,
			_ utils.Logger,
		) (quicSession, error) {
			cconn = connP
			hostname = hostnameP
			version = versionP
			conf = configP
			close(c)
			sess := NewMockQuicSession(mockCtrl)
			sess.EXPECT().run()
			return sess, nil
		}
		_, err := Dial(packetConn, addr, "quic.clemente.io:1337", nil, config)
		Expect(err).ToNot(HaveOccurred())
		Eventually(c).Should(BeClosed())
		Expect(cconn.(*conn).pconn).To(Equal(packetConn))
		Expect(hostname).To(Equal("quic.clemente.io"))
		Expect(version).To(Equal(config.Versions[0]))
		Expect(conf.Versions).To(Equal(config.Versions))
	})

	It("creates a new session when the server performs a retry", func() {
		manager := NewMockPacketHandlerManager(mockCtrl)
		mockMultiplexer.EXPECT().AddConn(packetConn, gomock.Any()).Return(manager, nil)
		mockMultiplexer.EXPECT().AddHandler(packetConn, gomock.Any(), gomock.Any())

		config := &Config{Versions: []protocol.VersionNumber{protocol.VersionTLS}}
		cl.config = config
		sess1 := NewMockQuicSession(mockCtrl)
		sess1.EXPECT().run().Return(handshake.ErrCloseSessionForRetry)
		sess2 := NewMockQuicSession(mockCtrl)
		sess2.EXPECT().run()
		sessions := []*MockQuicSession{sess1, sess2}
		newTLSClientSession = func(
			connP connection,
			_ sessionRunner,
			hostnameP string,
			versionP protocol.VersionNumber,
			_ protocol.ConnectionID,
			_ protocol.ConnectionID,
			configP *Config,
			tls handshake.MintTLS,
			paramsChan <-chan handshake.TransportParameters,
			_ protocol.PacketNumber,
			_ utils.Logger,
		) (quicSession, error) {
			sess := sessions[0]
			sessions = sessions[1:]
			return sess, nil
		}
		_, err := Dial(packetConn, addr, "quic.clemente.io:1337", nil, config)
		Expect(err).ToNot(HaveOccurred())
		Expect(sessions).To(BeEmpty())
	})

	It("only accepts one Retry packet", func() {
		manager := NewMockPacketHandlerManager(mockCtrl)
		mockMultiplexer.EXPECT().AddConn(packetConn, gomock.Any()).Return(manager, nil)
		mockMultiplexer.EXPECT().AddHandler(packetConn, gomock.Any(), gomock.Any())

		config := &Config{Versions: []protocol.VersionNumber{protocol.VersionTLS}}
		sess1 := NewMockQuicSession(mockCtrl)
		sess1.EXPECT().run().Return(handshake.ErrCloseSessionForRetry)
		// don't EXPECT any call to handlePacket()
		sess2 := NewMockQuicSession(mockCtrl)
		run := make(chan struct{})
		sess2.EXPECT().run().Do(func() { <-run })
		sessions := make(chan *MockQuicSession, 2)
		sessions <- sess1
		sessions <- sess2
		newTLSClientSession = func(
			connP connection,
			_ sessionRunner,
			hostnameP string,
			versionP protocol.VersionNumber,
			_ protocol.ConnectionID,
			_ protocol.ConnectionID,
			configP *Config,
			tls handshake.MintTLS,
			paramsChan <-chan handshake.TransportParameters,
			_ protocol.PacketNumber,
			_ utils.Logger,
		) (quicSession, error) {
			return <-sessions, nil
		}

		done := make(chan struct{})
		go func() {
			defer GinkgoRecover()
			_, err := Dial(packetConn, addr, "quic.clemente.io:1337", nil, config)
			Expect(err).ToNot(HaveOccurred())
			close(done)
		}()

		buf := &bytes.Buffer{}
		h := &wire.Header{
			IsLongHeader:     true,
			Type:             protocol.PacketTypeRetry,
			SrcConnectionID:  connID,
			DestConnectionID: connID,
			PacketNumberLen:  protocol.PacketNumberLen1,
		}
		err := h.Write(buf, protocol.PerspectiveServer, protocol.VersionTLS)
		Expect(err).ToNot(HaveOccurred())
		Eventually(sessions).Should(BeEmpty())
		packetConn.dataToRead <- buf.Bytes()
		time.Sleep(50 * time.Millisecond) // make sure the packet is read and discarded

		// make the go routine return
		close(run)
		Eventually(done).Should(BeClosed())
	})

	Context("Public Reset handling", func() {
		var (
			pr     []byte
			hdr    *wire.Header
			hdrLen int
		)

		BeforeEach(func() {
			cl.config = &Config{}

			pr = wire.WritePublicReset(cl.destConnID, 1, 0)
			r := bytes.NewReader(pr)
			iHdr, err := wire.ParseInvariantHeader(r, 0)
			Expect(err).ToNot(HaveOccurred())
			hdr, err = iHdr.Parse(r, protocol.PerspectiveServer, versionGQUICFrames)
			Expect(err).ToNot(HaveOccurred())
			hdrLen = r.Len()
		})

		It("closes the session when receiving a Public Reset", func() {
			sess := NewMockQuicSession(mockCtrl)
			sess.EXPECT().closeRemote(gomock.Any()).Do(func(err error) {
				Expect(err.(*qerr.QuicError).ErrorCode).To(Equal(qerr.PublicReset))
			})
			cl.session = sess
			cl.handlePacketImpl(&receivedPacket{
				remoteAddr: addr,
				header:     hdr,
				data:       pr[len(pr)-hdrLen:],
			})
		})

		It("ignores Public Resets from the wrong remote address", func() {
			cl.session = NewMockQuicSession(mockCtrl) // don't EXPECT any calls
			spoofedAddr := &net.UDPAddr{IP: net.IPv4(1, 2, 3, 4), Port: 5678}
			err := cl.handlePacketImpl(&receivedPacket{
				remoteAddr: spoofedAddr,
				header:     hdr,
				data:       pr[len(pr)-hdrLen:],
			})
			Expect(err).To(MatchError("Received a spoofed Public Reset"))
		})

		It("ignores unparseable Public Resets", func() {
			cl.session = NewMockQuicSession(mockCtrl) // don't EXPECT any calls
			err := cl.handlePacketImpl(&receivedPacket{
				remoteAddr: addr,
				header:     hdr,
				data:       pr[len(pr)-hdrLen : len(pr)-5], // cut off the last 5 bytes
			})
			Expect(err.Error()).To(ContainSubstring("Received a Public Reset. An error occurred parsing the packet"))
		})
	})
})

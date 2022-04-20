package ackhandler

import (
	"github.com/BGrewell/quic-go/internal/congestion"
	"github.com/BGrewell/quic-go/internal/protocol"
	"github.com/BGrewell/quic-go/internal/utils"
	"github.com/BGrewell/quic-go/logging"
)

// NewAckHandler creates a new SentPacketHandler and a new ReceivedPacketHandler
func NewAckHandler(
	initialPacketNumber protocol.PacketNumber,
	initialMaxDatagramSize protocol.ByteCount,
	rttStats *utils.RTTStats,
	pers protocol.Perspective,
	tracer logging.ConnectionTracer,
	logger utils.Logger,
	version protocol.VersionNumber,
	congestionAlgo congestion.CongestionAlgo,
) (SentPacketHandler, ReceivedPacketHandler) {
	sph := newSentPacketHandler(initialPacketNumber, initialMaxDatagramSize, rttStats, pers, tracer, logger, congestionAlgo)
	return sph, newReceivedPacketHandler(sph, rttStats, logger, version)
}

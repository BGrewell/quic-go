package congestion

import (
	"time"

	"github.com/BGrewell/quic-go/internal/protocol"
	"github.com/BGrewell/quic-go/internal/utils"
	"github.com/BGrewell/quic-go/logging"
)

const ()

type locoSender struct {
	hybridSlowStart HybridSlowStart
	rttStats        *utils.RTTStats
	cubic           *Cubic
	pacer           *pacer
	clock           Clock

	reno bool

	// Track the largest packet that has been sent.
	largestSentPacketNumber protocol.PacketNumber

	// Track the largest packet that has been acked.
	largestAckedPacketNumber protocol.PacketNumber

	// Track the largest packet number outstanding when a CWND cutback occurs.
	largestSentAtLastCutback protocol.PacketNumber

	// Whether the last loss event caused us to exit slowstart.
	// Used for stats collection of slowstartPacketsLost
	lastCutbackExitedSlowstart bool

	// Congestion window in bytes.
	congestionWindow protocol.ByteCount

	// Slow start congestion window in bytes, aka ssthresh.
	slowStartThreshold protocol.ByteCount

	// ACK counter for the Reno implementation.
	numAckedPackets uint64

	initialCongestionWindow    protocol.ByteCount
	initialMaxCongestionWindow protocol.ByteCount

	maxDatagramSize protocol.ByteCount

	lastState logging.CongestionState
	tracer    logging.ConnectionTracer
}

var (
	_ SendAlgorithm               = &locoSender{}
	_ SendAlgorithmWithDebugInfos = &locoSender{}
)

// NewLocoSender makes a new loco sender
func NewLocoSender(
	clock Clock,
	rttStats *utils.RTTStats,
	initialMaxDatagramSize protocol.ByteCount,
	reno bool,
	tracer logging.ConnectionTracer,
) *locoSender {
	return newLocoSender(
		clock,
		rttStats,
		reno,
		initialMaxDatagramSize,
		initialCongestionWindow*initialMaxDatagramSize,
		protocol.MaxCongestionWindowPackets*initialMaxDatagramSize,
		tracer,
	)
}

func newLocoSender(
	clock Clock,
	rttStats *utils.RTTStats,
	reno bool,
	initialMaxDatagramSize,
	initialCongestionWindow,
	initialMaxCongestionWindow protocol.ByteCount,
	tracer logging.ConnectionTracer,
) *locoSender {
	l := &locoSender{
		rttStats:                   rttStats,
		largestSentPacketNumber:    protocol.InvalidPacketNumber,
		largestAckedPacketNumber:   protocol.InvalidPacketNumber,
		largestSentAtLastCutback:   protocol.InvalidPacketNumber,
		initialCongestionWindow:    initialCongestionWindow,
		initialMaxCongestionWindow: initialMaxCongestionWindow,
		congestionWindow:           initialCongestionWindow,
		slowStartThreshold:         protocol.MaxByteCount,
		cubic:                      NewCubic(clock),
		clock:                      clock,
		reno:                       reno,
		tracer:                     tracer,
		maxDatagramSize:            initialMaxDatagramSize,
	}
	if l.tracer != nil {
		l.lastState = logging.CongestionStateSlowStart
		l.tracer.UpdatedCongestionState(logging.CongestionStateSlowStart)
	}
	return l
}

// TimeUntilSend returns when the next packet should be sent.
func (l *locoSender) TimeUntilSend(_ protocol.ByteCount) time.Time {
	// send now!
	return time.Time{}
}

func (l *locoSender) HasPacingBudget() bool {
	// we always have budget!
	return true
}

func (l *locoSender) maxCongestionWindow() protocol.ByteCount {
	// we allow up to 10,000 packets in flight!
	return l.maxDatagramSize * 10000
}

func (l *locoSender) minCongestionWindow() protocol.ByteCount {
	// we don't allow any less than 1,000 packets in flight!
	return l.maxDatagramSize * 1000
}

func (l *locoSender) OnPacketSent(
	sentTime time.Time,
	_ protocol.ByteCount,
	packetNumber protocol.PacketNumber,
	bytes protocol.ByteCount,
	isRetransmittable bool,
) {
	// we don't need to do any accounting here because we simply don't care!
}

func (l *locoSender) CanSend(bytesInFlight protocol.ByteCount) bool {
	// send it!!
	return true
}

func (l *locoSender) InRecovery() bool {
	// we don't believe in recovery
	return false
}

func (l *locoSender) InSlowStart() bool {
	// we only know one speed and that's fast!
	return false
}

func (l *locoSender) GetCongestionWindow() protocol.ByteCount {
	// we'll just say it's 10,000 packets in flight
	return l.maxDatagramSize * 1000000
}

func (l *locoSender) MaybeExitSlowStart() {
	// we don't care about any of this
}

func (l *locoSender) OnPacketAcked(
	ackedPacketNumber protocol.PacketNumber,
	ackedBytes protocol.ByteCount,
	priorInFlight protocol.ByteCount,
	eventTime time.Time,
) {
	// accounting's for tax guys
}

func (l *locoSender) OnPacketLost(packetNumber protocol.PacketNumber, lostBytes, priorInFlight protocol.ByteCount) {
	// we're like the USPS we don't lose anything and if we do we'll just deny it!!
}

// Called when we receive an ack. Normal TCP tracks how many packets one ack
// represents, but quic has a separate ack for each packet.
func (l *locoSender) maybeIncreaseCwnd(
	_ protocol.PacketNumber,
	ackedBytes protocol.ByteCount,
	priorInFlight protocol.ByteCount,
	eventTime time.Time,
) {
	// can't increase what's already booming!
}

func (l *locoSender) isCwndLimited(bytesInFlight protocol.ByteCount) bool {
	// we're never limiting
	return false
}

// BandwidthEstimate returns the current bandwidth estimate
func (l *locoSender) BandwidthEstimate() Bandwidth {
	// we'll estimate a little high
	return Bandwidth(100000000000)
}

// OnRetransmissionTimeout is called on an retransmission timeout
func (l *locoSender) OnRetransmissionTimeout(packetsRetransmitted bool) {
	// timeout's are for little kids
}

// OnConnectionMigration is called when the connection is migrated (?)
func (l *locoSender) OnConnectionMigration() {
}

func (l *locoSender) maybeTraceStateChange(new logging.CongestionState) {
	if l.tracer == nil || new == l.lastState {
		return
	}
	l.tracer.UpdatedCongestionState(new)
	l.lastState = new
}

func (l *locoSender) SetMaxDatagramSize(s protocol.ByteCount) {
	l.maxDatagramSize = s
}

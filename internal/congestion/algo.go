package congestion

import "strconv"

func _() {
	// An "invalid array index" compiler error signifies that the constant values have changed.
	// Re-run the stringer command to generate them again.
	var x [1]struct{}
	_ = x[ALGO_UNKNOWN-0]
	_ = x[ALGO_CUBIC-1]
}

const _CongestionAlgo_name = "ALGO_UNKNOWNALGO_CUBIC"

var _CongestionAlgo_index = [...]uint8{0, 12, 22}

func (i CongestionAlgo) String() string {
	if i < 0 || i >= CongestionAlgo(len(_CongestionAlgo_index)-1) {
		return "CongestionAlgo(" + strconv.FormatInt(int64(i), 10) + ")"
	}
	return _CongestionAlgo_name[_CongestionAlgo_index[i]:_CongestionAlgo_index[i+1]]
}

//go:generate stringer -type=CongestionAlgo
type CongestionAlgo int

const (
	ALGO_UNKNOWN CongestionAlgo = iota
	ALGO_CUBIC
)

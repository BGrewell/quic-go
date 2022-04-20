package logging

//go:generate sh -c "mockgen -package logging -self_package github.com/BGrewell/quic-go/logging -destination mock_connection_tracer_test.go github.com/BGrewell/quic-go/logging ConnectionTracer"
//go:generate sh -c "mockgen -package logging -self_package github.com/BGrewell/quic-go/logging -destination mock_tracer_test.go github.com/BGrewell/quic-go/logging Tracer"

package handler

import (
	"context"
	"errors"

	"github.com/voltik/voltik/backend/internal/api"
	"github.com/voltik/voltik/backend/internal/domain"
	"github.com/voltik/voltik/backend/internal/service"
)

// Page sizes of the contract.
const (
	defaultSeriesLimit = 1000
	maxSeriesLimit     = 5000
	defaultListLimit   = 50
	maxListLimit       = 200
)

func limitOr(v *int, def, max int) (int, error) {
	if v == nil {
		return def, nil
	}
	if *v < 1 || *v > max {
		return 0, domain.Invalid("limit", "is out of range")
	}
	return *v, nil
}

func cursorOf(c *string) string {
	if c == nil {
		return ""
	}
	return *c
}

func noBody() error { return &badRequest{message: "The request has no body."} }

// --- Glucose ------------------------------------------------------------------------

func (s *Server) ListGlucoseReadings(ctx context.Context, req api.ListGlucoseReadingsRequestObject) (api.ListGlucoseReadingsResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	limit, err := limitOr(req.Params.Limit, defaultSeriesLimit, maxSeriesLimit)
	if err != nil {
		return nil, err
	}
	items, next, err := s.healthData.ListReadings(ctx, a, req.Params.From, req.Params.To, limit, cursorOf(req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	out := api.GlucoseReadingPage{Items: make([]api.GlucoseReading, 0, len(items)), NextCursor: nextCursor(next)}
	for _, r := range items {
		out.Items = append(out.Items, toGlucoseReading(r))
	}
	return api.ListGlucoseReadings200JSONResponse(out), nil
}

func (s *Server) CreateGlucoseReading(ctx context.Context, req api.CreateGlucoseReadingRequestObject) (api.CreateGlucoseReadingResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, noBody()
	}
	r, created, err := s.healthData.CreateManualReading(ctx, a, domain.ReadingInput{
		IdempotencyKey: req.Body.IdempotencyKey, MeasuredAt: req.Body.MeasuredAt, ValueMgDl: req.Body.ValueMgDl,
	})
	if err != nil {
		return nil, err
	}
	if !created {
		return api.CreateGlucoseReading200JSONResponse(toGlucoseReading(r)), nil
	}
	return api.CreateGlucoseReading201JSONResponse(toGlucoseReading(r)), nil
}

func (s *Server) IngestGlucoseReadings(ctx context.Context, req api.IngestGlucoseReadingsRequestObject) (api.IngestGlucoseReadingsResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	if req.Body == nil || len(req.Body.Readings) == 0 || len(req.Body.Readings) > domain.MaxBatchItems {
		return nil, &badRequest{message: "Send between 1 and 500 readings."}
	}
	in := make([]domain.ReadingInput, len(req.Body.Readings))
	for i, r := range req.Body.Readings {
		in[i] = domain.ReadingInput{
			IdempotencyKey: r.IdempotencyKey, MeasuredAt: r.MeasuredAt, ValueMgDl: r.ValueMgDl,
			Source: string(r.Source), DeviceID: r.DeviceId, SignalQuality: r.SignalQuality,
		}
		if r.Trend != nil {
			in[i].Trend = string(*r.Trend)
		}
	}
	res, err := s.healthData.IngestGlucose(ctx, a, in)
	if err != nil {
		return nil, err
	}
	return api.IngestGlucoseReadings200JSONResponse(toBatchResult(res)), nil
}

func (s *Server) GetLatestGlucoseReading(ctx context.Context, _ api.GetLatestGlucoseReadingRequestObject) (api.GetLatestGlucoseReadingResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	r, err := s.healthData.LatestReading(ctx, a)
	if errors.Is(err, domain.ErrNotFound) {
		return api.GetLatestGlucoseReading204Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	return api.GetLatestGlucoseReading200JSONResponse(toGlucoseReading(r)), nil
}

func (s *Server) GetGlucoseSeries(ctx context.Context, req api.GetGlucoseSeriesRequestObject) (api.GetGlucoseSeriesResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	resolution := api.N15m
	if req.Params.Resolution != nil {
		if !req.Params.Resolution.Valid() {
			return nil, domain.Invalid("resolution", "must be 15m or 1h")
		}
		resolution = *req.Params.Resolution
	}
	points, err := s.healthData.Series(ctx, a, req.Params.From, req.Params.To, string(resolution))
	if err != nil {
		return nil, err
	}
	out := api.GlucoseSeries{Resolution: resolution, Items: make([]api.GlucoseSeriesPoint, 0, len(points))}
	for _, p := range points {
		out.Items = append(out.Items, api.GlucoseSeriesPoint{BucketStart: p.BucketStart.UTC(), MeanMgDl: p.Mean, MinMgDl: p.Min, MaxMgDl: p.Max, ReadingsCount: p.Count})
	}
	return api.GetGlucoseSeries200JSONResponse(out), nil
}

func (s *Server) UpdateGlucoseReading(ctx context.Context, req api.UpdateGlucoseReadingRequestObject) (api.UpdateGlucoseReadingResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	if req.Body == nil || (req.Body.ValueMgDl == nil && req.Body.MeasuredAt == nil) {
		return nil, &badRequest{message: "Send at least one field to change."}
	}
	r, err := s.healthData.UpdateManualReading(ctx, a, req.ReadingId, req.Body.ValueMgDl, req.Body.MeasuredAt)
	if err != nil {
		return nil, err
	}
	return api.UpdateGlucoseReading200JSONResponse(toGlucoseReading(r)), nil
}

func (s *Server) DeleteGlucoseReading(ctx context.Context, req api.DeleteGlucoseReadingRequestObject) (api.DeleteGlucoseReadingResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.healthData.DeleteManualReading(ctx, a, req.ReadingId); err != nil {
		return nil, err
	}
	return api.DeleteGlucoseReading204Response{}, nil
}

// --- Heart rate -----------------------------------------------------------------------

func (s *Server) ListHeartRateSamples(ctx context.Context, req api.ListHeartRateSamplesRequestObject) (api.ListHeartRateSamplesResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	limit, err := limitOr(req.Params.Limit, defaultSeriesLimit, maxSeriesLimit)
	if err != nil {
		return nil, err
	}
	items, next, err := s.healthData.ListHeartRate(ctx, a, req.Params.From, req.Params.To, limit, cursorOf(req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	out := api.HeartRateSamplePage{Items: make([]api.HeartRateSample, 0, len(items)), NextCursor: nextCursor(next)}
	for _, h := range items {
		out.Items = append(out.Items, toHeartRate(h))
	}
	return api.ListHeartRateSamples200JSONResponse(out), nil
}

func (s *Server) IngestHeartRateSamples(ctx context.Context, req api.IngestHeartRateSamplesRequestObject) (api.IngestHeartRateSamplesResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	if req.Body == nil || len(req.Body.Samples) == 0 || len(req.Body.Samples) > domain.MaxBatchItems {
		return nil, &badRequest{message: "Send between 1 and 500 samples."}
	}
	in := make([]domain.HeartRateInput, len(req.Body.Samples))
	for i, h := range req.Body.Samples {
		in[i] = domain.HeartRateInput{IdempotencyKey: h.IdempotencyKey, MeasuredAt: h.MeasuredAt, BPM: h.Bpm, DeviceID: h.DeviceId}
	}
	res, err := s.healthData.IngestHeartRate(ctx, a, in)
	if err != nil {
		return nil, err
	}
	return api.IngestHeartRateSamples200JSONResponse(toBatchResult(res)), nil
}

func (s *Server) GetLatestHeartRateSample(ctx context.Context, _ api.GetLatestHeartRateSampleRequestObject) (api.GetLatestHeartRateSampleResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	h, err := s.healthData.LatestHeartRate(ctx, a)
	if errors.Is(err, domain.ErrNotFound) {
		return api.GetLatestHeartRateSample204Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	return api.GetLatestHeartRateSample200JSONResponse(toHeartRate(h)), nil
}

// --- Devices --------------------------------------------------------------------------

func (s *Server) ListDevices(ctx context.Context, _ api.ListDevicesRequestObject) (api.ListDevicesResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	devices, err := s.healthData.ListDevices(ctx, a)
	if err != nil {
		return nil, err
	}
	out := api.DeviceList{Items: make([]api.Device, 0, len(devices))}
	for _, d := range devices {
		out.Items = append(out.Items, toDevice(d))
	}
	return api.ListDevices200JSONResponse(out), nil
}

func newDevice(b *api.DeviceCreate) domain.Device {
	return domain.Device{
		DeviceType: string(b.DeviceType), BrandModel: b.BrandModel, SerialOrMAC: b.SerialOrMac,
		BatteryPercent: b.BatteryPercent, SensorExpiresAt: utc(b.SensorExpiresAt),
	}
}

func (s *Server) RegisterDevice(ctx context.Context, req api.RegisterDeviceRequestObject) (api.RegisterDeviceResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, noBody()
	}
	d, created, err := s.healthData.RegisterDevice(ctx, a, newDevice(req.Body))
	if err != nil {
		return nil, err
	}
	if !created {
		return api.RegisterDevice200JSONResponse(toDevice(d)), nil
	}
	return api.RegisterDevice201JSONResponse(toDevice(d)), nil
}

func (s *Server) ReplaceSensor(ctx context.Context, req api.ReplaceSensorRequestObject) (api.ReplaceSensorResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, noBody()
	}
	d, err := s.healthData.ReplaceSensor(ctx, a, req.DeviceId, newDevice(req.Body))
	if err != nil {
		return nil, err
	}
	return api.ReplaceSensor201JSONResponse(toDevice(d)), nil
}

func (s *Server) UpdateDeviceStatus(ctx context.Context, req api.UpdateDeviceStatusRequestObject) (api.UpdateDeviceStatusResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	if b == nil || (b.ConnectionStatus == nil && !b.BatteryPercent.IsSpecified() && b.LastReadingAt == nil) {
		return nil, &badRequest{message: "Send at least one field to change."}
	}
	st := service.DeviceStatus{LastReadingAt: utc(b.LastReadingAt), SetBattery: b.BatteryPercent.IsSpecified()}
	if b.ConnectionStatus != nil {
		if !b.ConnectionStatus.Valid() {
			return nil, domain.Invalid("connection_status", "must be active or disconnected")
		}
		v := string(*b.ConnectionStatus)
		st.ConnectionStatus = &v
	}
	patchValue(b.BatteryPercent, &st.BatteryPercent)
	d, err := s.healthData.UpdateDeviceStatus(ctx, a, req.DeviceId, st)
	if err != nil {
		return nil, err
	}
	return api.UpdateDeviceStatus200JSONResponse(toDevice(d)), nil
}

func (s *Server) RemoveDevice(ctx context.Context, req api.RemoveDeviceRequestObject) (api.RemoveDeviceResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.healthData.RemoveDevice(ctx, a, req.DeviceId); err != nil {
		return nil, err
	}
	return api.RemoveDevice204Response{}, nil
}

func (s *Server) CreateNightscoutSecret(ctx context.Context, req api.CreateNightscoutSecretRequestObject) (api.CreateNightscoutSecretResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	secret, at, err := s.healthData.CreateNightscoutSecret(ctx, a, req.DeviceId)
	if err != nil {
		return nil, err
	}
	return api.CreateNightscoutSecret201JSONResponse(api.NightscoutSecret{
		NightscoutUrl: s.publicURL + "/v1/nightscout", ApiSecret: secret, CreatedAt: at.UTC(),
	}), nil
}

// --- Push tokens ------------------------------------------------------------------------

func toPushToken(t domain.PushToken) api.PushToken {
	return api.PushToken{Id: t.ID, Platform: api.PushPlatform(t.Platform), IsActive: t.IsActive, CreatedAt: t.CreatedAt.UTC(), UpdatedAt: t.UpdatedAt.UTC()}
}

func (s *Server) RegisterPushToken(ctx context.Context, req api.RegisterPushTokenRequestObject) (api.RegisterPushTokenResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, noBody()
	}
	t, created, err := s.healthData.RegisterPushToken(ctx, a, string(req.Body.Platform), req.Body.PushToken)
	if err != nil {
		return nil, err
	}
	if !created {
		return api.RegisterPushToken200JSONResponse(toPushToken(t)), nil
	}
	return api.RegisterPushToken201JSONResponse(toPushToken(t)), nil
}

func (s *Server) UnregisterPushToken(ctx context.Context, req api.UnregisterPushTokenRequestObject) (api.UnregisterPushTokenResponseObject, error) {
	a, err := account(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.healthData.UnregisterPushToken(ctx, a, req.PushTokenId); err != nil {
		return nil, err
	}
	return api.UnregisterPushToken204Response{}, nil
}

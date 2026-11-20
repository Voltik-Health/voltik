package handler_test

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/voltik/voltik/backend/internal/database/dbtest"
	"github.com/voltik/voltik/backend/internal/domain"
	"github.com/voltik/voltik/backend/internal/gemini"
	"github.com/voltik/voltik/backend/internal/retention"
	"github.com/voltik/voltik/backend/internal/simulator"
)

// --- Fakes of the services outside Voltik -----------------------------------------------

type quiet struct{}

func (quiet) Wake()                                                  {}
func (quiet) Notify(context.Context, []uuid.UUID, map[string]string) {}

// cannedAI answers every photo with a francesinha.
type cannedAI struct{}

func (*cannedAI) Configured() bool { return true }
func (*cannedAI) GenerateJSON(_ context.Context, _ string, _ *gemini.Image, _ map[string]any, out any) (string, error) {
	return "test-model", json.Unmarshal([]byte(`{"items":[{"name":"Pão de trigo","weight_g":100,"carbs_g":57,"calories_kcal":270}],
		"confidence_percent":72}`), out)
}

type fakeProducts struct{}

func (fakeProducts) Product(_ context.Context, code string) (domain.Food, bool, error) {
	switch code {
	case "5601234567890":
		g, unit := 100.0, "g"
		return domain.Food{Name: "Iogurte natural", Type: domain.FoodBrand, DataSource: domain.SourceOpenFoodFacts, BarcodeEAN: &code,
			Servings: []domain.FoodServing{{Description: "100 g", MetricAmount: &g, MetricUnit: &unit,
				Nutrition: domain.Nutrition{CarbsG: 5.2, CaloriesKcal: 61}}}}, true, nil
	case "5609999999999":
		return domain.Food{}, false, domain.ErrServiceUnavailable
	}
	return domain.Food{}, false, nil
}

// --- Helpers ---------------------------------------------------------------------------------

func at(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func id(m map[string]any) string { return m["id"].(string) }

func items(m map[string]any) []any { return m["items"].([]any) }

func field(v any, key string) any { return v.(map[string]any)[key] }

func patientID(c *client) string { return id(c.must(200, "GET", "/v1/me/patient", "")) }

// signIn signs in without becoming a patient (a caregiver).
func signIn(t *testing.T, api http.Handler, subject string) (*client, string) {
	c := &client{t: t, api: api}
	s := c.must(200, "POST", "/v1/auth/google", `{"id_token":"google:`+subject+`"}`)
	c.token = s["access_token"].(string)
	return c, s["user"].(map[string]any)["id"].(string)
}

func expectCode(t *testing.T, c *client, want int, code, method, path, body string) {
	t.Helper()
	status, out := c.call(method, path, body)
	if status != want || out["code"] != code {
		t.Fatalf("%s %s = %d %v, want %d %s", method, path, status, out, want, code)
	}
}

func readingJSON(key string, measured time.Time, value int, device string) string {
	d := ""
	if device != "" {
		d = `,"device_id":"` + device + `"`
	}
	return fmt.Sprintf(`{"idempotency_key":%q,"measured_at":%q,"value_mg_dl":%d,"source":"cgm_sensor","trend":"stable"%s}`, key, at(measured), value, d)
}

// --- Devices, readings and heart rate ------------------------------------------------------

func TestDevicesReadingsAndHeartRate(t *testing.T) {
	api := newFullAPI(t)
	manuel := signUpPatient(t, api, "manuel", "1968-03-02")
	rosa := signUpPatient(t, api, "rosa", "1955-07-19")

	sensor := manuel.must(201, "POST", "/v1/me/devices", `{"device_type":"cgm_sensor","brand_model":"FreeStyle Libre 3","serial_or_mac":"LIB-1"}`)
	if sensor["connection_status"] != "warming_up" {
		t.Fatalf("a new CGM sensor warms up: %v", sensor)
	}
	if again := manuel.must(200, "POST", "/v1/me/devices", `{"device_type":"cgm_sensor","brand_model":"FreeStyle Libre 3","serial_or_mac":"LIB-1"}`); id(again) != id(sensor) {
		t.Fatal("the same serial must return the same device")
	}
	expectCode(t, manuel, 409, "active_sensor_exists", "POST", "/v1/me/devices", `{"device_type":"cgm_sensor","brand_model":"Dexcom G7","serial_or_mac":"DX-1"}`)
	watch := manuel.must(201, "POST", "/v1/me/devices", `{"device_type":"smartwatch","brand_model":"Galaxy Watch6","serial_or_mac":"D4:3A"}`)

	now := time.Now().UTC().Truncate(time.Minute)
	batch := `{"readings":[` + strings.Join([]string{
		readingJSON("r1", now.Add(-10*time.Minute), 120, id(sensor)),
		readingJSON("r2", now.Add(-5*time.Minute), 125, id(sensor)),
		readingJSON("r3", now.Add(10*time.Minute), 130, id(sensor)),
		readingJSON("r4", now.Add(-5*time.Minute), 130, uuid.NewString()),
		`{"idempotency_key":"r5","measured_at":"` + at(now) + `","value_mg_dl":110,"source":"manual_fingerstick","device_id":"` + id(sensor) + `"}`,
	}, ",") + `]}`
	res := manuel.must(200, "POST", "/v1/me/glucose-readings/batch", batch)
	rejected := res["rejected"].([]any)
	if res["inserted"] != 2.0 || res["duplicates"] != 0.0 || len(rejected) != 3 ||
		field(rejected[0], "code") != "measured_at_in_future" || field(rejected[1], "code") != "unknown_device" || field(rejected[2], "code") != "validation_failed" {
		t.Fatalf("batch = %v", res)
	}
	if again := manuel.must(200, "POST", "/v1/me/glucose-readings/batch", batch); again["inserted"] != 0.0 || again["duplicates"] != 2.0 {
		t.Fatalf("resending must change nothing: %v", again)
	}
	devices := items(manuel.must(200, "GET", "/v1/me/devices", ""))
	if field(devices[0], "connection_status") != "active" || field(devices[0], "first_reading_at") != at(now.Add(-10*time.Minute)) {
		t.Fatalf("the sensor becomes active with its first reading: %v", devices[0])
	}
	if latest := manuel.must(200, "GET", "/v1/me/glucose-readings/latest", ""); latest["value_mg_dl"] != 125.0 {
		t.Fatalf("latest = %v", latest)
	}
	from, to := at(now.Add(-time.Hour)), at(now.Add(time.Hour))
	page := manuel.must(200, "GET", "/v1/me/glucose-readings?from="+from+"&to="+to+"&limit=1", "")
	if len(items(page)) != 1 || page["next_cursor"] == nil {
		t.Fatalf("page 1 = %v", page)
	}
	page = manuel.must(200, "GET", "/v1/me/glucose-readings?from="+from+"&to="+to+"&limit=1&cursor="+page["next_cursor"].(string), "")
	if len(items(page)) != 1 || field(items(page)[0], "value_mg_dl") != 125.0 || page["next_cursor"] != nil {
		t.Fatalf("page 2 = %v", page)
	}
	if series := manuel.must(200, "GET", "/v1/me/glucose-readings/series?from="+from+"&to="+to+"&resolution=1h", ""); len(items(series)) == 0 {
		t.Fatalf("series = %v", series)
	}
	expectCode(t, manuel, 400, "validation_failed", "GET", "/v1/me/glucose-readings/series?from="+to+"&to="+from, "")

	hr := fmt.Sprintf(`{"samples":[{"idempotency_key":"h1","measured_at":%q,"bpm":92,"device_id":%q},
		{"idempotency_key":"h2","measured_at":%q,"bpm":300,"device_id":%q}]}`, at(now.Add(-time.Minute)), id(watch), at(now), id(watch))
	if res := manuel.must(200, "POST", "/v1/me/heart-rate/batch", hr); res["inserted"] != 1.0 || len(res["rejected"].([]any)) != 1 {
		t.Fatalf("heart rate batch = %v", res)
	}
	if latest := manuel.must(200, "GET", "/v1/me/heart-rate/latest", ""); latest["bpm"] != 92.0 {
		t.Fatalf("latest heart rate = %v", latest)
	}

	// Rosa sees nothing of Manuel's and cannot write to his devices
	if len(items(rosa.must(200, "GET", "/v1/me/devices", ""))) != 0 {
		t.Fatal("rosa sees manuel's devices")
	}
	res = rosa.must(200, "POST", "/v1/me/glucose-readings/batch", `{"readings":[`+readingJSON("x1", now, 100, id(sensor))+`]}`)
	if field(res["rejected"].([]any)[0], "code") != "unknown_device" {
		t.Fatalf("rosa wrote to manuel's sensor: %v", res)
	}
	rosa.must(204, "GET", "/v1/me/glucose-readings/latest", "")

	// Replace the sensor, then remove the watch
	newSensor := manuel.must(201, "POST", "/v1/me/devices/"+id(sensor)+"/replace", `{"device_type":"cgm_sensor","brand_model":"FreeStyle Libre 3","serial_or_mac":"LIB-2"}`)
	if newSensor["connection_status"] != "warming_up" {
		t.Fatalf("new sensor = %v", newSensor)
	}
	expectCode(t, manuel, 409, "invalid_state", "POST", "/v1/me/devices/"+id(sensor)+"/replace", `{"device_type":"cgm_sensor","brand_model":"X","serial_or_mac":"LIB-3"}`)
	manuel.must(204, "DELETE", "/v1/me/devices/"+id(watch), "")
	if n := len(items(manuel.must(200, "GET", "/v1/me/devices", ""))); n != 2 {
		t.Fatalf("devices after removing the watch = %d, want 2 (old and new sensor)", n)
	}
	status := manuel.must(200, "PATCH", "/v1/me/devices/"+id(newSensor)+"/status", `{"battery_percent":58,"last_reading_at":"`+at(now)+`"}`)
	if status["connection_status"] != "active" || status["battery_percent"] != 58.0 {
		t.Fatalf("status = %v", status)
	}
}

func TestManualReadings(t *testing.T) {
	api := newFullAPI(t)
	manuel := signUpPatient(t, api, "manuel", "1968-03-02")
	now := time.Now().UTC().Truncate(time.Minute)
	body := `{"idempotency_key":"m1","measured_at":"` + at(now) + `","value_mg_dl":142}`
	r := manuel.must(201, "POST", "/v1/me/glucose-readings", body)
	if r["source"] != "manual_fingerstick" || r["device_id"] != nil {
		t.Fatalf("manual reading = %v", r)
	}
	if again := manuel.must(200, "POST", "/v1/me/glucose-readings", body); again["id"] != r["id"] {
		t.Fatal("the same key must return the same reading")
	}
	path := fmt.Sprintf("/v1/me/glucose-readings/%.0f", r["id"].(float64))
	if fixed := manuel.must(200, "PATCH", path, `{"value_mg_dl":124}`); fixed["value_mg_dl"] != 124.0 {
		t.Fatalf("corrected = %v", fixed)
	}
	manuel.must(204, "DELETE", path, "")
	manuel.must(204, "GET", "/v1/me/glucose-readings/latest", "")

	sensor := manuel.must(201, "POST", "/v1/me/devices", `{"device_type":"cgm_sensor","brand_model":"Libre","serial_or_mac":"S1"}`)
	manuel.must(200, "POST", "/v1/me/glucose-readings/batch", `{"readings":[`+readingJSON("s1", now, 120, id(sensor))+`]}`)
	latest := manuel.must(200, "GET", "/v1/me/glucose-readings/latest", "")
	expectCode(t, manuel, 403, "reading_immutable", "PATCH", fmt.Sprintf("/v1/me/glucose-readings/%.0f", latest["id"].(float64)), `{"value_mg_dl":100}`)
}

// --- Alerts, receipts and escalation ---------------------------------------------------------

func TestCriticalAlertsAndEscalation(t *testing.T) {
	api := newFullAPI(t)
	manuel := signUpPatient(t, api, "manuel", "1968-03-02")
	ines, inesID := signIn(t, api, "ines")
	rosa := signUpPatient(t, api, "rosa", "1955-07-19")
	pid := patientID(manuel)
	dbtest.Exec(t, `INSERT INTO caregiver_links (patient_id, caregiver_user_id, permission_level, status, linked_at)
		VALUES ($1, $2, 'alerts_only', 'active', now())`, pid, inesID)
	dbtest.Exec(t, `UPDATE users SET phone = '+351912000000' WHERE id = $1`, inesID)
	dbtest.Exec(t, `INSERT INTO emergency_contacts (patient_id, full_name, phone, call_priority, notify_by_sms)
		VALUES ($1, 'Ana', '+351913000000', 1, TRUE)`, pid)

	sensor := manuel.must(201, "POST", "/v1/me/devices", `{"device_type":"cgm_sensor","brand_model":"Libre","serial_or_mac":"S1"}`)
	now := time.Now().UTC().Truncate(time.Second)
	manuel.must(200, "POST", "/v1/me/glucose-readings/batch", `{"readings":[`+readingJSON("low1", now.Add(-time.Minute), 52, id(sensor))+`]}`)
	manuel.must(200, "POST", "/v1/me/glucose-readings/batch", `{"readings":[`+readingJSON("low2", now, 48, id(sensor))+`,`+
		readingJSON("old-high", now.Add(-3*time.Hour), 320, id(sensor))+`]}`)

	alerts := items(manuel.must(200, "GET", "/v1/me/alerts", ""))
	if len(alerts) != 1 {
		t.Fatalf("one critical alert expected (repeat window, old readings ignored): %v", alerts)
	}
	alert := alerts[0].(map[string]any)
	if alert["alert_type"] != "critical_low" || alert["ack_status"] != "pending" || alert["message"] != "A glicemia está em 48 mg/dL." || alert["source_value_mg_dl"] != 52.0 {
		t.Fatalf("alert = %v", alert)
	}
	if n := len(alert["recipients"].([]any)); n != 4 {
		t.Fatalf("recipients = %d, want push and in_app for manuel and ines", n)
	}

	// Escalation: caregivers at minute 3, then the emergency contacts
	escalate := func() {
		dbtest.Exec(t, `UPDATE alerts SET next_escalation_at = now() - interval '1 second' WHERE next_escalation_at IS NOT NULL`)
		api.alerts.Escalate(context.Background())
	}
	escalate()
	if n := dbtest.Count(t, `SELECT count(*) FROM sms_messages WHERE alert_recipient_id IS NOT NULL AND escalation_step = 1`); n != 1 {
		t.Fatalf("SMS to the caregiver = %d", n)
	}
	escalate()
	if n := dbtest.Count(t, `SELECT count(*) FROM sms_messages WHERE emergency_contact_id IS NOT NULL AND phone = '+351913000000'`); n != 1 {
		t.Fatalf("SMS to the emergency contact = %d", n)
	}
	escalate()
	if n := dbtest.Count(t, `SELECT count(*) FROM alerts WHERE next_escalation_at IS NOT NULL`); n != 0 {
		t.Fatal("the escalation ends when the contacts run out")
	}
	body := dbtest.Count(t, `SELECT count(*) FROM sms_messages WHERE body LIKE '%Test manuel%' AND body LIKE '%52 mg/dL%' AND body NOT LIKE '%dose%'`)
	if body != 2 {
		t.Fatalf("the texts name the patient and the value, never a dose (%d)", body)
	}

	// Ines, a caregiver, acknowledges; rosa is not a recipient
	expectCode(t, rosa, 404, "not_found", "POST", "/v1/me/alerts/"+id(alert)+"/acknowledge", "")
	acked := ines.must(200, "POST", "/v1/me/alerts/"+id(alert)+"/acknowledge", "")
	if acked["ack_status"] != "acknowledged" || field(acked["acknowledged_by"], "user_id") != inesID {
		t.Fatalf("acknowledged = %v", acked)
	}
	closed := manuel.must(200, "POST", "/v1/me/alerts/"+id(alert)+"/resolve", `{"status":"resolved","resolution_notes":"Comi um sumo."}`)
	if closed["ack_status"] != "resolved" || closed["resolution_notes"] != "Comi um sumo." {
		t.Fatalf("resolved = %v", closed)
	}
	if again := manuel.must(200, "POST", "/v1/me/alerts/"+id(alert)+"/resolve", `{"status":"dismissed"}`); again["ack_status"] != "resolved" {
		t.Fatal("a closed alert stays closed")
	}

	// Receipts: only the person's own notifications, and never backwards
	var mine, theirs string
	for _, r := range closed["recipients"].([]any) {
		switch {
		case field(r, "channel") == "in_app" && field(r, "user_id") != inesID:
			mine = field(r, "id").(string)
		case field(r, "user_id") == inesID:
			theirs = field(r, "id").(string)
		}
	}
	manuel.must(204, "POST", "/v1/me/notifications/"+mine+"/read", "")
	manuel.must(204, "POST", "/v1/me/notifications/"+mine+"/delivered", "")
	expectCode(t, manuel, 404, "not_found", "POST", "/v1/me/notifications/"+theirs+"/read", "")

	// Filters
	if n := len(items(manuel.must(200, "GET", "/v1/me/alerts?ack_status=pending", ""))); n != 0 {
		t.Fatalf("pending alerts = %d", n)
	}
	expectCode(t, manuel, 400, "validation_failed", "GET", "/v1/me/alerts?severity=huge", "")
}

func TestSensorMonitor(t *testing.T) {
	api := newFullAPI(t)
	manuel := signUpPatient(t, api, "manuel", "1968-03-02")
	sensor := manuel.must(201, "POST", "/v1/me/devices", `{"device_type":"cgm_sensor","brand_model":"Libre","serial_or_mac":"S1",
		"sensor_expires_at":"`+at(time.Now().Add(10*time.Hour))+`"}`)
	now := time.Now().UTC()
	manuel.must(200, "POST", "/v1/me/glucose-readings/batch", `{"readings":[`+readingJSON("r1", now.Add(-30*time.Minute), 120, id(sensor))+`]}`)
	dbtest.Exec(t, `UPDATE devices SET last_synced_at = now() - interval '25 minutes' WHERE id = $1`, id(sensor))

	api.alerts.CheckSensors(context.Background())
	api.alerts.CheckSensors(context.Background()) // twice: still one alert of each type
	alerts := items(manuel.must(200, "GET", "/v1/me/alerts", ""))
	types := map[any]int{}
	for _, a := range alerts {
		types[field(a, "alert_type")]++
	}
	if types["sensor_disconnected"] != 1 || types["sensor_expiring"] != 1 {
		t.Fatalf("alerts = %v", types)
	}
	if d := items(manuel.must(200, "GET", "/v1/me/devices", ""))[0]; field(d, "connection_status") != "disconnected" {
		t.Fatalf("device = %v", d)
	}
	// Readings again: the sensor is active and the alert resolved by itself (UC092 A1)
	manuel.must(200, "POST", "/v1/me/glucose-readings/batch", `{"readings":[`+readingJSON("r2", time.Now().UTC(), 118, id(sensor))+`]}`)
	if n := len(items(manuel.must(200, "GET", "/v1/me/alerts?alert_type=sensor_disconnected&ack_status=resolved", ""))); n != 1 {
		t.Fatalf("resolved disconnection alerts = %d", n)
	}
}

// --- Food catalogue, meals, insulin and activities ------------------------------------------

func TestFoodsAndMeals(t *testing.T) {
	api := newFullAPI(t)
	manuel := signUpPatient(t, api, "manuel", "1968-03-02")
	rosa := signUpPatient(t, api, "rosa", "1955-07-19")
	admin, adminID := signIn(t, api, "admin")
	dbtest.Exec(t, `UPDATE users SET is_platform_admin = TRUE WHERE id = $1`, adminID)

	expectCode(t, manuel, 403, "forbidden", "POST", "/v1/admin/food-categories", `{"name":"Pão"}`)
	cat := admin.must(201, "POST", "/v1/admin/food-categories", `{"name":"Pão, cereais e bolos"}`)
	expectCode(t, admin, 409, "category_name_taken", "POST", "/v1/admin/food-categories", `{"name":"Pão, cereais e bolos"}`)
	bread := admin.must(201, "POST", "/v1/admin/foods", `{"food_name":"Pão de trigo","food_type":"generic","category_id":"`+id(cat)+`",
		"servings":[{"serving_description":"100 g","metric_amount":100,"metric_unit":"g","carbs_g":57,"calories_kcal":270,"fiber_g":3.1},
		            {"serving_description":"1 pão (50 g)","metric_amount":50,"metric_unit":"g","carbs_g":28.5,"calories_kcal":135}]}`)
	if bread["is_verified"] != true || bread["data_source"] != "portuguese_traditional" {
		t.Fatalf("admin food = %v", bread)
	}
	plate := field(bread["servings"].([]any)[0], "id").(string)
	if field(bread["servings"].([]any)[0], "serving_description") != "1 pão (50 g)" {
		t.Fatalf("servings are ordered by amount: %v", bread["servings"])
	}

	// Accent-insensitive search
	if found := items(manuel.must(200, "GET", "/v1/foods?q=pao", "")); len(found) != 1 || field(found[0], "food_name") != "Pão de trigo" {
		t.Fatalf("search = %v", found)
	}
	expectCode(t, manuel, 400, "validation_failed", "GET", "/v1/foods?q=p", "")

	// A custom food: only its author sees it until a reviewer verifies it.
	broa := manuel.must(201, "POST", "/v1/foods", `{"food_name":"Broa da avó","servings":[{"serving_description":"1 fatia (50 g)","carbs_g":24,"calories_kcal":120,"fiber_g":30}]}`)
	if broa["is_own"] != true || broa["is_verified"] != false {
		t.Fatalf("custom food = %v", broa)
	}
	if len(items(rosa.must(200, "GET", "/v1/foods?q=broa", ""))) != 0 {
		t.Fatal("rosa sees manuel's food before it is verified")
	}
	admin.must(200, "POST", "/v1/admin/foods/"+id(broa)+"/verify", "")
	if len(items(rosa.must(200, "GET", "/v1/foods?q=broa", ""))) != 1 {
		t.Fatal("a verified food is visible to everyone")
	}
	expectCode(t, manuel, 400, "validation_failed", "POST", "/v1/foods", `{"food_name":"X","servings":[{"serving_description":"1","carbs_g":10,"sugars_g":20,"calories_kcal":10}]}`)

	// Barcodes: Open Food Facts once, then the catalogue
	yog := manuel.must(200, "GET", "/v1/foods/barcode/5601234567890", "")
	if yog["data_source"] != "open_food_facts" || yog["source_attribution"] == nil {
		t.Fatalf("product = %v", yog)
	}
	if again := rosa.must(200, "GET", "/v1/foods/barcode/5601234567890", ""); id(again) != id(yog) {
		t.Fatal("the product must be saved")
	}
	expectCode(t, manuel, 404, "not_found", "GET", "/v1/foods/barcode/5600000000001", "")
	expectCode(t, manuel, 503, "service_unavailable", "GET", "/v1/foods/barcode/5609999999999", "")
	expectCode(t, manuel, 409, "barcode_in_use", "POST", "/v1/foods", `{"food_name":"Cópia","barcode_ean":"5601234567890","servings":[{"serving_description":"100 g","carbs_g":5,"calories_kcal":60}]}`)

	// A meal: the server works out the carbohydrates of each item
	now := time.Now().UTC().Truncate(time.Second)
	mealBody := `{"idempotency_key":"meal-1","meal_type":"lunch","status":"eaten","eaten_at":"` + at(now.Add(-time.Hour)) + `",
		"items":[{"serving_id":"` + plate + `","serving_quantity":2}]}`
	meal := manuel.must(201, "POST", "/v1/me/meals", mealBody)
	if meal["total_carbs_g"] != 57.0 || meal["total_calories_kcal"] != 270.0 || field(items(meal)[0], "food_name") != "Pão de trigo" {
		t.Fatalf("meal = %v", meal)
	}
	if again := manuel.must(200, "POST", "/v1/me/meals", mealBody); id(again) != id(meal) {
		t.Fatal("a resent meal must not be stored twice")
	}
	itemID := id(items(meal)[0].(map[string]any))
	edited := manuel.must(200, "PUT", "/v1/me/meals/"+id(meal)+"/items", `{"items":[{"id":"`+itemID+`","serving_id":"`+plate+`","serving_quantity":1},
		{"serving_id":"`+field(yog["servings"].([]any)[0], "id").(string)+`","is_eaten":false}]}`)
	if edited["total_carbs_g"] != 28.5 || len(items(edited)) != 2 {
		t.Fatalf("edited meal = %v (an uneaten item does not count)", edited)
	}
	expectCode(t, manuel, 409, "invalid_state", "PATCH", "/v1/me/meals/"+id(meal), `{"total_carbs_g":10}`)
	expectCode(t, rosa, 404, "not_found", "GET", "/v1/me/meals/"+id(meal), "")
	expectCode(t, rosa, 400, "validation_failed", "POST", "/v1/me/meals", `{"meal_type":"lunch","status":"eaten","items":[{"serving_id":"`+id(broa)+`"}]}`)

	// A planned meal with only the total, then eaten; an eaten meal cannot be cancelled
	today := domain.LisbonDate(now).Format("2006-01-02")
	planned := manuel.must(201, "POST", "/v1/me/meals", `{"meal_type":"dinner","status":"planned","meal_date":"`+today+`","planned_time":"20:30","total_carbs_g":45}`)
	if planned["eaten_at"] != nil || planned["planned_time"] != "20:30" {
		t.Fatalf("planned = %v", planned)
	}
	eaten := manuel.must(200, "POST", "/v1/me/meals/"+id(planned)+"/eaten", `{}`)
	if eaten["status"] != "eaten" || eaten["eaten_at"] == nil {
		t.Fatalf("eaten = %v", eaten)
	}
	expectCode(t, manuel, 409, "invalid_state", "POST", "/v1/me/meals/"+id(planned)+"/cancel", "")

	// Insulin linked to the meal, with the daily totals
	dose := manuel.must(201, "POST", "/v1/me/insulin-logs", `{"idempotency_key":"dose-1","insulin_type":"rapid_bolus","dose_units":4.04,
		"administered_at":"`+at(now.Add(-61*time.Minute))+`","reason":"meal","meal_id":"`+id(meal)+`"}`)
	if dose["dose_units"] != 4.0 {
		t.Fatalf("dose = %v (one decimal place)", dose)
	}
	expectCode(t, rosa, 400, "validation_failed", "POST", "/v1/me/insulin-logs", `{"idempotency_key":"dose-2","insulin_type":"rapid_bolus","dose_units":4,
		"administered_at":"`+at(now)+`","reason":"meal","meal_id":"`+id(meal)+`"}`)
	list := manuel.must(200, "GET", "/v1/me/insulin-logs?from="+at(now.Add(-24*time.Hour))+"&to="+at(now.Add(time.Hour)), "")
	if len(items(list)) != 1 || field(list["daily_totals"].([]any)[0], "total_units") != 4.0 {
		t.Fatalf("insulin = %v", list)
	}
	day := manuel.must(200, "GET", "/v1/me/meals?date="+domain.LisbonDate(now.Add(-time.Hour)).Format("2006-01-02"), "")
	found := false
	for _, m := range items(day) {
		if id(m.(map[string]any)) == id(meal) && len(field(m, "insulin_logs").([]any)) == 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("the diary shows the meal with its dose: %v", day)
	}

	// Activities: by hand, and a watch batch with a duplicate and an unknown device
	manuel.must(201, "POST", "/v1/me/activities", `{"idempotency_key":"a1","activity_name":"Caminhada","started_at":"`+at(now.Add(-2*time.Hour))+`","duration_minutes":35,"intensity":"moderate"}`)
	res := manuel.must(200, "POST", "/v1/me/activities/batch", `{"activities":[
		{"idempotency_key":"a1","activity_name":"Caminhada","started_at":"`+at(now.Add(-2*time.Hour))+`","duration_minutes":35,"intensity":"moderate"},
		{"idempotency_key":"a2","activity_name":"Corrida","started_at":"`+at(now.Add(-3*time.Hour))+`","duration_minutes":20,"intensity":"vigorous","device_id":"`+uuid.NewString()+`"},
		{"idempotency_key":"a3","activity_name":"Natação","started_at":"`+at(now.Add(-4*time.Hour))+`","duration_minutes":0,"intensity":"vigorous"}]}`)
	if res["inserted"] != 0.0 || res["duplicates"] != 1.0 || len(res["rejected"].([]any)) != 2 {
		t.Fatalf("activity batch = %v", res)
	}
	stats := manuel.must(200, "GET", "/v1/me/stats?from="+at(now.Add(-24*time.Hour))+"&to="+at(now.Add(time.Hour)), "")
	if stats["active_minutes"] != 35.0 || stats["time_in_range_percent"] != nil {
		t.Fatalf("stats without readings = %v", stats)
	}

	manuel.must(204, "DELETE", "/v1/me/meals/"+id(meal), "")
	expectCode(t, manuel, 404, "not_found", "GET", "/v1/me/meals/"+id(meal), "")
	if n := dbtest.Count(t, `SELECT count(*) FROM insulin_logs WHERE meal_id = $1 AND deleted_at IS NULL`, id(meal)); n != 1 {
		t.Fatal("the dose stays when its meal is deleted")
	}
}

// --- Statistics, reports and the PDF -------------------------------------------------------

func TestReportsAndPDF(t *testing.T) {
	api := newFullAPI(t)
	manuel := signUpPatient(t, api, "manuel", "1968-03-02")
	pid := patientID(manuel)
	// Yesterday, in Portugal: a reading every 5 minutes, 6 hours of them low
	yesterday := domain.LisbonDate(time.Now()).AddDate(0, 0, -1)
	start := domain.DayStart(yesterday)
	dbtest.Exec(t, `
		INSERT INTO glucose_readings (patient_id, measured_at, value_mg_dl, trend, source, idempotency_key)
		SELECT $1, $2::timestamptz + make_interval(mins => 5 * g), CASE WHEN g < 72 THEN 60 ELSE 130 END,
		       'stable', 'cgm_sensor', 'rep-' || g
		  FROM generate_series(0, 287) AS g`, pid, start)

	day := yesterday.Format("2006-01-02")
	report := manuel.must(201, "POST", "/v1/me/reports", `{"period_type":"day","start_date":"`+day+`","end_date":"`+day+`"}`)
	if report["time_below_range_percent"] != 25.0 || report["time_in_range_percent"] != 75.0 || report["coverage_percent"] != 100.0 ||
		report["hypo_episodes"] != 1.0 || report["low_coverage"] != false || report["target_min_mg_dl"] != 70.0 {
		t.Fatalf("report = %v", report)
	}
	if report["iris_summary"] != nil {
		t.Fatal("no Iris summary without the external_ai_processing consent")
	}
	expectCode(t, manuel, 409, "no_readings", "POST", "/v1/me/reports", `{"period_type":"day","start_date":"2026-01-01","end_date":"2026-01-01"}`)
	expectCode(t, manuel, 400, "validation_failed", "POST", "/v1/me/reports", `{"period_type":"custom","start_date":"2026-01-01","end_date":"2026-06-01"}`)
	if list := items(manuel.must(200, "GET", "/v1/me/reports", "")); len(list) != 1 {
		t.Fatalf("reports = %v", list)
	}

	link := manuel.must(200, "GET", "/v1/reports/"+id(report)+"/pdf", "")
	url := strings.TrimPrefix(link["url"].(string), "http://voltik.test")
	req := httptest.NewRequest("GET", url, nil) // no Authorization: the link is enough
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, req)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/pdf" || !bytes.HasPrefix(rec.Body.Bytes(), []byte("%PDF-")) {
		t.Fatalf("PDF download = %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	if again := manuel.must(200, "GET", "/v1/reports/"+id(report)+"/pdf", ""); again["url"] == nil ||
		dbtest.Count(t, `SELECT count(*) FROM stored_files WHERE purpose = 'report_pdf'`) != 1 {
		t.Fatal("the PDF is generated once")
	}
	rec = httptest.NewRecorder()
	api.ServeHTTP(rec, httptest.NewRequest("GET", url[:len(url)-4]+"0000", nil))
	if rec.Code != 403 {
		t.Fatalf("a tampered link = %d, want 403", rec.Code)
	}

	manuel.must(204, "DELETE", "/v1/me/reports/"+id(report), "")
	if n := dbtest.Count(t, `SELECT count(*) FROM stored_files`); n != 0 {
		t.Fatal("deleting a report erases its PDF")
	}
	dbtest.Exec(t, `INSERT INTO clinical_reports (patient_id, period_type, start_date, end_date, generated_by_role,
		time_in_range_percent, time_below_range_percent, time_above_range_percent, mean_glucose_mg_dl)
		VALUES ($1, 'day', $2, $2, 'system_scheduled', 75, 25, 0, 112)`, pid, yesterday)
	if list := items(manuel.must(200, "GET", "/v1/me/reports", "")); len(list) != 1 || field(list[0], "target_min_mg_dl") != 70.0 {
		t.Fatalf("an old report without its range shows the defaults: %v", list)
	}
}

// --- Meal photos -----------------------------------------------------------------------------

func pngPhoto(t *testing.T) []byte {
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 40, 30))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func upload(t *testing.T, api http.Handler, token, mealID string, data []byte) (int, map[string]any) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, _ := w.CreateFormFile("photo", "prato.png")
	_, _ = part.Write(data)
	_ = w.Close()
	req := httptest.NewRequest("POST", "/v1/me/meals/"+mealID+"/photos", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestMealPhotos(t *testing.T) {
	api := newFullAPI(t)
	manuel := signUpPatient(t, api, "manuel", "1968-03-02")
	admin, adminID := signIn(t, api, "admin")
	dbtest.Exec(t, `UPDATE users SET is_platform_admin = TRUE WHERE id = $1`, adminID)
	admin.must(201, "POST", "/v1/admin/foods", `{"food_name":"Pão de trigo","food_type":"generic",
		"servings":[{"serving_description":"100 g","metric_amount":100,"metric_unit":"g","carbs_g":57,"calories_kcal":270}]}`)
	meal := manuel.must(201, "POST", "/v1/me/meals", `{"meal_type":"lunch","status":"eaten"}`)

	if status, out := upload(t, api, manuel.token, id(meal), pngPhoto(t)); status != 403 || out["code"] != "consent_required" {
		t.Fatalf("without the AI consent = %d %v", status, out)
	}
	manuel.must(201, "POST", "/v1/me/consents", `{"consent_type":"external_ai_processing","document_version":"1.0"}`)
	if status, out := upload(t, api, manuel.token, id(meal), []byte("not an image at all")); status != 415 {
		t.Fatalf("not an image = %d %v", status, out)
	}
	status, photo := upload(t, api, manuel.token, id(meal), pngPhoto(t))
	if status != 202 || photo["analysis_status"] != "pending" || !strings.Contains(photo["image_url"].(string), "signature=") {
		t.Fatalf("upload = %d %v", status, photo)
	}
	expectCode(t, manuel, 409, "analysis_not_ready", "POST", "/v1/me/meals/"+id(meal)+"/photos/"+id(photo)+"/confirm", `{"items":[]}`)

	api.diary.AnalyzePhotos(context.Background())
	done := manuel.must(200, "GET", "/v1/me/meals/"+id(meal)+"/photos/"+id(photo), "")
	proposed := done["proposed_items"].([]any)
	if done["analysis_status"] != "completed" || len(proposed) != 1 || field(proposed[0], "serving_quantity") != 1.0 || done["low_confidence"] != false {
		t.Fatalf("analysis = %v", done)
	}
	confirmed := manuel.must(200, "POST", "/v1/me/meals/"+id(meal)+"/photos/"+id(photo)+"/confirm",
		`{"items":[{"serving_id":"`+field(proposed[0], "serving_id").(string)+`","serving_quantity":1.5}]}`)
	if confirmed["total_carbs_g"] != 85.5 || len(items(confirmed)) != 1 {
		t.Fatalf("confirmed meal = %v", confirmed)
	}
	expectCode(t, manuel, 409, "invalid_state", "POST", "/v1/me/meals/"+id(meal)+"/photos/"+id(photo)+"/confirm", `{"items":[]}`)
	if n := dbtest.Count(t, `SELECT count(*) FROM stored_files WHERE purpose = 'meal_photo' AND content_type = 'image/jpeg'`); n != 1 {
		t.Fatal("the photo is stored as JPEG")
	}
}

// --- Nightscout uploads (Juggluco, xDrip+) ---------------------------------------------------

func TestNightscoutUploads(t *testing.T) {
	api := newFullAPI(t)
	manuel := signUpPatient(t, api, "manuel", "1968-03-02")
	sensor := manuel.must(201, "POST", "/v1/me/devices", `{"device_type":"cgm_sensor","brand_model":"FreeStyle Libre 3 (Juggluco)","serial_or_mac":"J1"}`)
	watch := manuel.must(201, "POST", "/v1/me/devices", `{"device_type":"smartwatch","brand_model":"Watch","serial_or_mac":"W1"}`)
	expectCode(t, manuel, 409, "invalid_state", "POST", "/v1/me/devices/"+id(watch)+"/nightscout-secret", "")
	ns := manuel.must(201, "POST", "/v1/me/devices/"+id(sensor)+"/nightscout-secret", "")
	if ns["nightscout_url"] != "http://voltik.test/v1/nightscout" || len(ns["api_secret"].(string)) != 16 {
		t.Fatalf("secret = %v", ns)
	}
	sum := sha1.Sum([]byte(ns["api_secret"].(string)))

	post := func(secret, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/v1/nightscout/api/v1/entries", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("api-secret", secret)
		rec := httptest.NewRecorder()
		api.ServeHTTP(rec, req)
		return rec
	}
	ms := time.Now().Add(-time.Minute).UnixMilli()
	entries := fmt.Sprintf(`[{"type":"sgv","sgv":112,"date":%d,"direction":"FortyFiveDown","device":"Juggluco"},
		{"type":"sgv","sgv":10,"date":%d},{"type":"cal","date":%d}]`, ms, ms-60000, ms)
	if rec := post("wrong", entries); rec.Code != 401 {
		t.Fatalf("a wrong secret = %d", rec.Code)
	}
	rec := post(hex.EncodeToString(sum[:]), entries)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"sgv":112`) || strings.Contains(rec.Body.String(), `"sgv":10,`) {
		t.Fatalf("upload = %d %s", rec.Code, rec.Body.String())
	}
	if again := post(ns["api_secret"].(string), entries); again.Code != 200 || dbtest.Count(t, `SELECT count(*) FROM glucose_readings`) != 1 {
		t.Fatal("resending, with the plain secret, stores nothing new")
	}
	latest := manuel.must(200, "GET", "/v1/me/glucose-readings/latest", "")
	if latest["value_mg_dl"] != 112.0 || latest["trend"] != "falling" || latest["device_id"] != id(sensor) {
		t.Fatalf("reading = %v", latest)
	}
	req := httptest.NewRequest("GET", "/v1/nightscout/api/v1/entries.json?count=5", nil)
	req.Header.Set("api-secret", hex.EncodeToString(sum[:]))
	get := httptest.NewRecorder()
	api.ServeHTTP(get, req)
	if get.Code != 200 || !strings.Contains(get.Body.String(), `"direction":"FortyFiveDown"`) {
		t.Fatalf("entries = %d %s", get.Code, get.Body.String())
	}
	status := httptest.NewRecorder()
	api.ServeHTTP(status, httptest.NewRequest("GET", "/v1/nightscout/api/v1/status.json", nil))
	if status.Code != 200 || !strings.Contains(status.Body.String(), `"status":"ok"`) {
		t.Fatalf("status = %d", status.Code)
	}
	// A new secret stops the old one
	manuel.must(201, "POST", "/v1/me/devices/"+id(sensor)+"/nightscout-secret", "")
	if rec := post(hex.EncodeToString(sum[:]), entries); rec.Code != 401 {
		t.Fatalf("the old secret still works: %d", rec.Code)
	}
}

// --- Push tokens -----------------------------------------------------------------------------

func TestPushTokens(t *testing.T) {
	api := newFullAPI(t)
	manuel := signUpPatient(t, api, "manuel", "1968-03-02")
	ines, _ := signIn(t, api, "ines")
	tok := manuel.must(201, "POST", "/v1/me/push-tokens", `{"platform":"android","push_token":"fcm-1"}`)
	manuel.must(200, "POST", "/v1/me/push-tokens", `{"platform":"android","push_token":"fcm-1"}`)
	moved := ines.must(200, "POST", "/v1/me/push-tokens", `{"platform":"android","push_token":"fcm-1"}`)
	if id(moved) != id(tok) {
		t.Fatal("the token of a shared phone moves to the new user")
	}
	expectCode(t, manuel, 404, "not_found", "DELETE", "/v1/me/push-tokens/"+id(tok), "")
	ines.must(204, "DELETE", "/v1/me/push-tokens/"+id(tok), "")
	expectCode(t, manuel, 400, "validation_failed", "POST", "/v1/me/push-tokens", `{"platform":"android","push_token":"  "}`)
}

// --- Simulator ----------------------------------------------------------------------------------

func TestSimulatorFillsTheHistoryOnce(t *testing.T) {
	api := newFullAPI(t)
	manuel := signUpPatient(t, api, "manuel", "1968-03-02")
	manuel.must(201, "POST", "/v1/me/devices", `{"device_type":"cgm_sensor","brand_model":"Voltik CGM simulator","serial_or_mac":"SIM-0001"}`)
	traces, err := simulator.LoadTraces()
	if err != nil {
		t.Fatal(err)
	}
	sim := simulator.New(api.store, api.health, traces, slog.New(slog.NewTextHandler(io.Discard, nil)))
	sim.Tick(context.Background())
	first := dbtest.Count(t, `SELECT count(*) FROM glucose_readings`)
	if first < 30*288 || first > 30*288+2 {
		t.Fatalf("readings after the first run = %d, want 30 days of 5-minute slots", first)
	}
	sim.Tick(context.Background())
	if n := dbtest.Count(t, `SELECT count(*) FROM glucose_readings`); n > first+1 {
		t.Fatalf("a second run added %d readings", n-first)
	}
	if n := dbtest.Count(t, `SELECT count(*) FROM alerts a JOIN glucose_readings g ON g.id = a.source_reading_id
		WHERE g.measured_at < now() - interval '2 hours'`); n != 0 {
		t.Fatal("old simulated readings raise no alerts")
	}
	if d := items(manuel.must(200, "GET", "/v1/me/devices", ""))[0]; field(d, "connection_status") != "active" {
		t.Fatalf("the simulated sensor is active: %v", d)
	}
}

// ADR 0006: erasing an account takes its health data, and now also its
// files (meal photos and report PDFs live in stored_files, ON DELETE CASCADE).
func TestErasureTakesTheHealthDataAndFiles(t *testing.T) {
	api := newFullAPI(t)
	manuel := signUpPatient(t, api, "manuel", "1968-03-02")
	manuel.must(201, "POST", "/v1/me/consents", `{"consent_type":"external_ai_processing","document_version":"1.0"}`)
	sensor := manuel.must(201, "POST", "/v1/me/devices", `{"device_type":"cgm_sensor","brand_model":"Libre","serial_or_mac":"S1"}`)
	manuel.must(200, "POST", "/v1/me/glucose-readings/batch", `{"readings":[`+readingJSON("low", time.Now().UTC(), 50, id(sensor))+`]}`)
	meal := manuel.must(201, "POST", "/v1/me/meals", `{"meal_type":"lunch","status":"eaten"}`)
	if status, _ := upload(t, api, manuel.token, id(meal), pngPhoto(t)); status != 202 {
		t.Fatalf("upload = %d", status)
	}
	custom := manuel.must(201, "POST", "/v1/foods", `{"food_name":"Broa","servings":[{"serving_description":"1 fatia","carbs_g":24,"calories_kcal":120}]}`)

	manuel.must(204, "DELETE", "/v1/me", "")
	if _, err := retention.NewJob(api.store, slog.New(slog.NewTextHandler(io.Discard, nil))).
		Run(context.Background(), time.Now().Add(domain.RetentionPeriod+time.Hour)); err != nil {
		t.Fatal(err)
	}
	for table, want := range map[string]int{
		"patients": 0, "glucose_readings": 0, "alerts": 0, "alert_recipients": 0, "meals": 0, "meal_photos": 0, "stored_files": 0,
	} {
		if n := dbtest.Count(t, `SELECT count(*) FROM `+table); n != want {
			t.Fatalf("%s: %d rows left after the erasure", table, n)
		}
	}
	if n := dbtest.Count(t, `SELECT count(*) FROM foods WHERE id = $1 AND created_by_user_id IS NULL`, id(custom)); n != 1 {
		t.Fatal("a custom food stays, without its author")
	}
}

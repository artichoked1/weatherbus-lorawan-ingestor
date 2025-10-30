package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/jackc/pgx/v5/pgxpool"
)

//--- Debug ---//

var debug bool

func debugf(format string, args ...any) {
	if debug {
		log.Printf("[DEBUG] "+format, args...)
	}
}

//--- JSON types ---//

type UpCommon struct {
	EndDeviceIDs struct {
		DeviceID string `json:"device_id"`
		DevEUI   string `json:"dev_eui"`
		AppIDs   struct {
			AppID string `json:"application_id"`
		} `json:"application_ids"`
	} `json:"end_device_ids"`

	ReceivedAt    time.Time `json:"received_at"`
	UplinkMessage UplinkMsg `json:"uplink_message"`
}

type UplinkMsg struct {
	FPort          int            `json:"f_port"`
	DecodedPayload DecodedPayload `json:"decoded_payload"`
	RxMetadata     []RxMetadata   `json:"rx_metadata"`
	Settings       UplinkSettings `json:"settings"`
	ReceivedAt     time.Time      `json:"received_at"`
}

type DecodedPayload struct {
	Slaves []struct {
		ID      int `json:"id"`
		Sensors []struct {
			Format int     `json:"format"`
			Index  int     `json:"index"`
			Type   int     `json:"type"`
			Value  float64 `json:"value"`
		} `json:"sensors"`
	} `json:"slaves"`
}

type RxMetadata struct {
	GatewayIDs struct {
		GatewayID string `json:"gateway_id"`
		EUI       string `json:"eui"`
	} `json:"gateway_ids"`
	RSSI     *int       `json:"rssi"`
	SNR      *float64   `json:"snr"`
	Time     *time.Time `json:"time"`
	Location *struct {
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
	} `json:"location"`
}

type UplinkSettings struct {
	DataRate struct {
		Lora struct {
			Bandwidth       *int   `json:"bandwidth"`
			SpreadingFactor *int   `json:"spreading_factor"`
			CodingRate      string `json:"coding_rate"`
		} `json:"lora"`
	} `json:"data_rate"`
	Frequency string `json:"frequency"`
}

type DirectUp struct {
	UpCommon
	Simulated *bool `json:"simulated"`
}

type Parsed struct {
	When         time.Time
	StationEUI   string
	StationDevID string
	AppID        string
	Msg          UplinkMsg
}

func parseUplink(b []byte) (*Parsed, error) {
	// Direct /up only
	var du DirectUp
	if err := json.Unmarshal(b, &du); err == nil && du.EndDeviceIDs.DevEUI != "" {
		when := du.UplinkMessage.ReceivedAt
		if when.IsZero() {
			when = du.ReceivedAt
		}
		if when.IsZero() {
			when = time.Now().UTC()
		}
		debugf("parsed direct /up for dev_eui: %s", du.EndDeviceIDs.DevEUI)
		return &Parsed{
			When:         when.UTC(),
			StationEUI:   strings.ToUpper(du.EndDeviceIDs.DevEUI),
			StationDevID: du.EndDeviceIDs.DeviceID,
			AppID:        du.EndDeviceIDs.AppIDs.AppID,
			Msg:          du.UplinkMessage,
		}, nil
	}

	if debug {
		if len(b) > 2048 {
			log.Printf("[DEBUG] payload head: %s", string(b[:2048]))
		} else {
			log.Printf("[DEBUG] payload: %s", string(b))
		}
	}
	return nil, fmt.Errorf("unknown TTN uplink shape (expecting direct /up)")
}

// --- Sensor type validation ---//
var validSensorTypes = map[int]struct{}{
	1: {}, 2: {}, 3: {}, 4: {}, 5: {}, 6: {}, 7: {}, 8: {},
	9: {}, 10: {}, 11: {}, 12: {}, 13: {}, 14: {}, 15: {},
}

// --- SQL statements ---//
const insertMeasurementSQL = `
INSERT INTO measurements(
  time, station_eui, station_devid, slave_id, sensor_type, sensor_index, value, format, gateway_id, latitude, longitude
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
ON CONFLICT DO NOTHING;
`

const upsertStationSQL = `
INSERT INTO stations(station_eui, application_id, station_devid)
VALUES ($1,$2,$3)
ON CONFLICT (station_eui) DO UPDATE
SET application_id = EXCLUDED.application_id,
    station_devid  = EXCLUDED.station_devid;
`

const upsertGatewaySQL = `
INSERT INTO gateways(gateway_id, gateway_eui)
VALUES ($1,$2)
ON CONFLICT (gateway_id) DO UPDATE SET gateway_eui = EXCLUDED.gateway_eui;
`

//--- Helpers ---//

// Fails if the env var is not set
func mustEnv(k string) string {
	v := os.Getenv(k)
	if v == "" {
		log.Fatalf("missing env %s", k)
	}
	return v
}

// Returns the env var value or a default value if not set
func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func nullFloat(f *float64) *float64 { return f }

func randSuffix() string { return fmt.Sprintf("%d", time.Now().UnixNano()%1e9) }

// --- MQTT handler ---//
func handleMessage(ctx context.Context, pool *pgxpool.Pool, msg mqtt.Message) {
	if debug {
		log.Printf("[DEBUG] mqtt topic: %s qos: %d retained: %v", msg.Topic(), msg.Qos(), msg.Retained())
	}

	p, err := parseUplink(msg.Payload())
	if err != nil {
		log.Printf("parse error: %v", err)
		return
	}

	if p.AppID != "" && p.StationEUI != "" {
		if _, err := pool.Exec(ctx, upsertStationSQL,
			p.StationEUI, p.AppID, nullIfEmpty(p.StationDevID)); err != nil {
			log.Printf("station upsert error: %v", err)
		}
	}

	// Gateway/location
	var gwID string
	var lat, lon *float64
	if len(p.Msg.RxMetadata) > 0 {
		rm := p.Msg.RxMetadata[0]
		gwID = rm.GatewayIDs.GatewayID
		if gwID != "" {
			if _, err := pool.Exec(ctx, upsertGatewaySQL, gwID, rm.GatewayIDs.EUI); err != nil {
				log.Printf("gateway upsert error: %v", err)
			}
		}
		if rm.Location != nil {
			latV, lonV := rm.Location.Latitude, rm.Location.Longitude
			lat, lon = &latV, &lonV
		}
	}

	count := 0
	for _, s := range p.Msg.DecodedPayload.Slaves {
		for _, m := range s.Sensors {
			if _, ok := validSensorTypes[m.Type]; !ok {
				debugf("skip unknown sensor type: %d idx: %d value: %v", m.Type, m.Index, m.Value)
				continue
			}
			_, err := pool.Exec(ctx, insertMeasurementSQL,
				p.When, p.StationEUI, nullIfEmpty(p.StationDevID), s.ID, m.Type, m.Index, m.Value, m.Format,
				nullIfEmpty(gwID), nullFloat(lat), nullFloat(lon),
			)
			if err != nil {
				log.Printf("insert error: %v (eui: %s slave: %d type:%d idx: %d)", err, p.StationEUI, s.ID, m.Type, m.Index)
				continue
			}
			count++
		}
	}

	log.Printf("ingested %d measurements from %s", count, p.StationEUI)
}

func main() {
	flag.BoolVar(&debug, "debug", false, "enable debug logging")
	flag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	pgdsn := mustEnv("PG_DSN")
	appID := mustEnv("TTN_APP_ID")
	apiKey := mustEnv("TTN_API_KEY")
	host := mustEnv("TTN_REGION_HOST") // e.g. au1.cloud.thethings.network
	port := envOr("TTN_MQTT_PORT", "1883")
	authEnabled := envOr("MQTT_USE_AUTH", "true")
	protocol := envOr("TTN_MQTT_PROTOCOL", "mqtt") // mqtt or mqtts
	topic := mustEnv("MQTT_TOPIC")

	// DB pool
	pool, err := pgxpool.New(ctx, pgdsn)
	if err != nil {
		log.Fatalf("pgx pool: %v", err)
	}
	defer pool.Close()

	// MQTT client options
	opts := mqtt.NewClientOptions().
		AddBroker(protocol + "://" + host + ":" + port).
		SetClientID("ttn-uplink-ingestor-" + randSuffix())

	if strings.HasPrefix(protocol, "mqtts") {
		opts.SetTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12})
	}

	if authEnabled == "true" {
		opts.SetUsername(appID)
		opts.SetPassword(apiKey)
	}

	opts.SetAutoReconnect(true)
	opts.SetConnectionLostHandler(func(_ mqtt.Client, err error) {
		log.Printf("mqtt connection lost: %v", err)
	})
	opts.SetOnConnectHandler(func(c mqtt.Client) {
		if token := c.Subscribe(topic, 0, func(_ mqtt.Client, msg mqtt.Message) {
			handleMessage(ctx, pool, msg)
		}); token.Wait() && token.Error() != nil {
			log.Printf("subscribe error: %v", token.Error())
		} else {
			log.Printf("subscribed to %s", topic)
		}
	})

	client := mqtt.NewClient(opts)
	if token := client.Connect(); token.Wait() && token.Error() != nil {
		log.Fatalf("mqtt connect: %v", token.Error())
	}

	log.Println("ingestor running. Ctrl+C to exit.")
	<-ctx.Done()
	log.Println("shutdown signal received")
	client.Disconnect(250)
}

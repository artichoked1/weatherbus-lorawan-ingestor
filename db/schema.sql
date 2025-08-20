-- Enable Timescale
CREATE EXTENSION IF NOT EXISTS timescaledb;

-- Sensor types table
CREATE TABLE IF NOT EXISTS sensor_types (
  type_id SMALLINT PRIMARY KEY,
  name    TEXT NOT NULL,
  unit    TEXT NOT NULL
);

INSERT INTO sensor_types (type_id, name, unit) VALUES
  (1,  'air_temperature_c',           '°C'),
  (2,  'humidity_prh',                '%RH'),
  (3,  'pressure_pa',                 'Pa'),
  (4,  'wind_speed_mps',              'm/s'),
  (5,  'wind_direction_deg',          'deg'),
  (6,  'cumulative_rainfall_mm',      'mm'),
  (7,  'solar_radiation_w_m2',        'W/m²'),
  (8,  'uv_index',                    'index'),
  (9,  'light_intensity_lux',         'lux'),
  (10, 'air_quality_ppm',             'ppm'),
  (11, 'soil_moisture_percent',       '%'),
  (12, 'soil_temperature_c',          '°C'),
  (13, 'canopy_temperature_c',        '°C'),
  (14, 'water_temperature_c',         '°C'),
  (15, 'water_level_cm',              'cm')
ON CONFLICT DO NOTHING;

-- Stations table
CREATE TABLE IF NOT EXISTS stations (
  station_eui TEXT PRIMARY KEY,              -- e.g. "70B3D57ED0069153"
  application_id TEXT NOT NULL,              -- e.g. "openclimate"
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Slaves table
CREATE TABLE IF NOT EXISTS slaves (
  station_eui TEXT NOT NULL REFERENCES stations(station_eui) ON DELETE CASCADE,
  slave_id    INTEGER NOT NULL,
  vendor_id   SMALLINT,
  product_id  SMALLINT,
  PRIMARY KEY (station_eui, slave_id)
);

-- Gateways table
CREATE TABLE IF NOT EXISTS gateways (
  gateway_id TEXT PRIMARY KEY,               -- e.g. "artichoketech-noarlunga-01"
  gateway_eui TEXT
);

-- Measurements hypertable
ALTER TABLE measurements (
  time          TIMESTAMPTZ NOT NULL,
  station_eui   TEXT NOT NULL,
  station_devid TEXT,
  slave_id      INTEGER NOT NULL,
  sensor_type   SMALLINT NOT NULL,
  sensor_index  SMALLINT NOT NULL,
  value         DOUBLE PRECISION NOT NULL,
  format        SMALLINT,
  gateway_id    TEXT,
  latitude      DOUBLE PRECISION,
  longitude     DOUBLE PRECISION,
  UNIQUE (time, station_eui, slave_id, sensor_type, sensor_index)
);

SELECT create_hypertable('measurements', 'time', if_not_exists => TRUE);

-- Helpful indexes
CREATE INDEX IF NOT EXISTS ix_measurements_station_time
  ON measurements (station_eui, time DESC);
CREATE INDEX IF NOT EXISTS ix_measurements_sensor
  ON measurements (sensor_type, time DESC);

-- Uplink table for RF stats
CREATE TABLE IF NOT EXISTS uplinks (
  event_time    TIMESTAMPTZ NOT NULL,
  station_eui   TEXT NOT NULL,
  gateway_id    TEXT,
  rssi          INTEGER,
  snr           DOUBLE PRECISION,
  frequency_hz  BIGINT,
  sf            SMALLINT,
  bandwidth_hz  INTEGER,
  coding_rate   TEXT,
  latitude      DOUBLE PRECISION,
  longitude     DOUBLE PRECISION
);
SELECT create_hypertable('uplinks', 'event_time', if_not_exists => TRUE);

-- Hourly aggregate
CREATE MATERIALIZED VIEW IF NOT EXISTS measurements_hourly
WITH (timescaledb.continuous) AS
SELECT time_bucket('1 hour', time) AS bucket,
       station_eui, slave_id, sensor_type, sensor_index,
       avg(value) AS avg_value, min(value) AS min_value, max(value) AS max_value
FROM measurements
GROUP BY bucket, station_eui, slave_id, sensor_type, sensor_index;

SELECT add_continuous_aggregate_policy('measurements_hourly',
  start_offset => INTERVAL '7 days',
  end_offset   => INTERVAL '1 hour',
  schedule_interval => INTERVAL '15 minutes');

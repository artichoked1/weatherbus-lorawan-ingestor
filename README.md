# WeatherBus TTN Ingestor

This repository contains a few things:
1. **Payload formatter** - Javascript that runs on TTN cloud and decodes compact WeatherBus payloads to JSON.
2. **Ingestor service** - A Go program that subscribes to either TTN's MQTT broker or a seperate one (if using the weather station in WiFi mode and connecting to a broker).
3. **Database schema** - An SQL schema for the sensor data tables in TimescaleDB

# This is a Work In Progress (WIP)

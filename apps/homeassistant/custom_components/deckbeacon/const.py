"""Constants for the DeckBeacon integration."""

from __future__ import annotations

from typing import Final

DOMAIN: Final = "deckbeacon"
INTEGRATION_VERSION: Final = "0.1.0"
PROTOCOL_VERSION: Final = 1

PATH_PAIR: Final = "/api/deckbeacon/v1/pair"
PATH_SESSION: Final = "/api/deckbeacon/v1/ws"

PAIRING_WINDOW_SECONDS: Final = 600
HANDSHAKE_TIMEOUT: Final = 10.0
HELLO_TIMEOUT: Final = 10.0
HEARTBEAT_SECONDS: Final = 15
MAX_FRAME: Final = 65535 + 1024
MAX_PREAUTH_PER_IP: Final = 4
MAX_PREAUTH_TOTAL: Final = 64

CONF_DEVICE_ID: Final = "device_id"
CONF_DEVICE_STATIC: Final = "device_static"
CONF_NAME: Final = "name"
CONF_MODEL: Final = "model"
CONF_VENDOR: Final = "vendor"
CONF_METRIC_KEYS: Final = "metric_keys"

EVENT_DEVICE: Final = "deckbeacon_event"
SIGNAL_UPDATE: Final = "deckbeacon_update_{}"
SIGNAL_NEW_METRIC: Final = "deckbeacon_new_metric_{}"

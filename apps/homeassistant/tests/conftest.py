"""Shared fixtures."""

from __future__ import annotations

import pytest


@pytest.fixture(autouse=True)
def auto_enable_custom_integrations(enable_custom_integrations):
    """Allow loading custom_components/deckbeacon in every test."""
    return


@pytest.fixture
async def client(hass, hass_client_no_auth):
    """HTTP up, DeckBeacon hub loaded, unauthenticated aiohttp client to HA."""
    from homeassistant.setup import async_setup_component

    from custom_components.deckbeacon.hub import async_get_hub

    assert await async_setup_component(hass, "http", {})
    # Real HA loads websocket_api at startup; it must exist before the test
    # server freezes the router (DeckBeacon depends on it for the card API).
    assert await async_setup_component(hass, "websocket_api", {})
    assert await async_setup_component(hass, "image", {})
    assert await async_setup_component(hass, "brands", {})
    await async_get_hub(hass)
    return await hass_client_no_auth()

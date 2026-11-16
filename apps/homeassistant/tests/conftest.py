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
    await async_get_hub(hass)
    return await hass_client_no_auth()

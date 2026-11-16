"""DeckBeacon: Home Assistant companion for SteamOS devices."""

from __future__ import annotations

from homeassistant.config_entries import ConfigEntry
from homeassistant.const import Platform
from homeassistant.core import HomeAssistant

from .const import DOMAIN
from .hub import DeviceRuntime, async_get_hub

PLATFORMS: list[Platform] = [Platform.SENSOR, Platform.BINARY_SENSOR]

type DeckBeaconConfigEntry = ConfigEntry[DeviceRuntime]


async def async_setup_entry(hass: HomeAssistant, entry: DeckBeaconConfigEntry) -> bool:
    hub = await async_get_hub(hass)
    runtime = DeviceRuntime(hass, entry)
    await runtime.async_load()
    entry.runtime_data = runtime
    hub.runtimes[entry.entry_id] = runtime
    await hass.config_entries.async_forward_entry_setups(entry, PLATFORMS)
    return True


async def async_unload_entry(hass: HomeAssistant, entry: DeckBeaconConfigEntry) -> bool:
    ok = await hass.config_entries.async_unload_platforms(entry, PLATFORMS)
    hub = hass.data.get(DOMAIN)
    if ok and hub is not None:
        runtime = hub.runtimes.pop(entry.entry_id, None)
        if runtime is not None:
            await runtime.async_flush()
            if runtime.channel is not None:
                await runtime.channel.ws.close(code=1012, message=b"integration reloading")
    return ok


async def async_remove_entry(hass: HomeAssistant, entry: ConfigEntry) -> None:
    """Deleting the device in HA revokes it (AC8): its key is no longer accepted."""
    hub = await async_get_hub(hass)
    await hub.revoke(entry)

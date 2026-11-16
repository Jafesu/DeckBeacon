"""Spike config flow: enrol one device public key. NOT PRODUCTION (no SAS pairing)."""
from __future__ import annotations

import base64

import voluptuous as vol

from homeassistant import config_entries

from . import DOMAIN


class SpikeConfigFlow(config_entries.ConfigFlow, domain=DOMAIN):
    VERSION = 1

    async def async_step_user(self, user_input=None):
        errors = {}
        if user_input is not None:
            try:
                if len(base64.b64decode(user_input["public_key"], validate=True)) != 32:
                    raise ValueError
            except ValueError:
                errors["public_key"] = "invalid_key"
            else:
                await self.async_set_unique_id(user_input["device_id"])
                self._abort_if_unique_id_configured()
                return self.async_create_entry(title=f"Spike {user_input['device_id']}", data=user_input)
        return self.async_show_form(
            step_id="user",
            data_schema=vol.Schema({vol.Required("device_id"): str, vol.Required("public_key"): str}),
            errors=errors,
        )

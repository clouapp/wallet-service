package config

import "github.com/macrowallets/waas/app/facades"

func registerRamps() {
	facades.Config().Add("ramps", map[string]any{"kyc_intent_material_retention_hours": envInt("KYC_INTENT_MATERIAL_RETENTION_HOURS", 720), "bridge_base_url": envString("RAMP_BRIDGE_BASE_URL", ""), "ozav_base_url": envString("RAMP_OZAV_BASE_URL", ""), "blindpay_base_url": envString("RAMP_BLINDPAY_BASE_URL", "")})
}

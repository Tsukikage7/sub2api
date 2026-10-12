//go:build unit

package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestSettingService_BrandingAcrossPublicAndAdminSettings(t *testing.T) {
	cases := []struct {
		name     string
		siteName string
		logo     string
		wantName string
		wantLogo string
	}{
		{name: "new installation", wantName: "ai-gateway", wantLogo: "/logo.jpg"},
		{name: "legacy defaults", siteName: "Sub2API", logo: "/logo.svg", wantName: "ai-gateway", wantLogo: "/logo.jpg"},
		{name: "legacy spelling", siteName: " sub2api ", wantName: "ai-gateway", wantLogo: "/logo.jpg"},
		{name: "custom branding", siteName: "Team Gateway", logo: "https://example.com/custom.png", wantName: "Team Gateway", wantLogo: "https://example.com/custom.png"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			values := map[string]string{SettingKeySiteName: tc.siteName, SettingKeySiteLogo: tc.logo}
			svc := NewSettingService(&settingPublicRepoStub{values: values}, &config.Config{})
			public, err := svc.GetPublicSettings(t.Context())
			require.NoError(t, err)
			require.Equal(t, tc.wantName, public.SiteName)
			require.Equal(t, tc.wantLogo, public.SiteLogo)

			admin := svc.parseSettings(values)
			require.Equal(t, tc.wantName, admin.SiteName)
			require.Equal(t, tc.wantLogo, admin.SiteLogo)

			raw, err := svc.GetPublicSettingsForInjection(t.Context())
			require.NoError(t, err)
			payload := raw.(*PublicSettingsInjectionPayload)
			require.Equal(t, tc.wantName, payload.SiteName)
			require.Equal(t, tc.wantLogo, payload.SiteLogo)
			require.Equal(t, tc.siteName, values[SettingKeySiteName])
			require.Equal(t, tc.logo, values[SettingKeySiteLogo])
		})
	}
}

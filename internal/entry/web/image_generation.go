package web

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/voocel/ainovel-cli/internal/imagejob"
)

func (c *v2Controller) imageGenerationSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		settings, err := c.imageConfig.LoadSettings()
		if err != nil {
			envelopeErr(w, http.StatusInternalServerError, codeConfigInvalid, err)
			return
		}
		envelope(w, http.StatusOK, 0, settings, "")
	case http.MethodPut:
		var settings imagejob.Settings
		if err := decodeBody(r, &settings); err != nil {
			envelopeErr(w, http.StatusBadRequest, codeInvalidRequest, err)
			return
		}
		settings = imagejob.NormalizeSettings(settings)
		if err := imagejob.ValidateSettings(settings); err != nil {
			envelopeErr(w, http.StatusUnprocessableEntity, codeConfigInvalid, err)
			return
		}
		for scene, profileID := range map[string]string{"novel": settings.Novel.DefaultProfileID, "chat": settings.Chat.DefaultProfileID, "play": settings.Play.DefaultProfileID} {
			if profileID == "" {
				continue
			}
			if _, err := c.imageConfig.LoadProfile(profileID); err != nil {
				envelopeErr(w, http.StatusUnprocessableEntity, codeConfigInvalid, fmt.Errorf("%s default profile %q does not exist", scene, profileID))
				return
			}
		}
		if err := c.imageConfig.SaveSettings(settings); err != nil {
			envelopeErr(w, http.StatusInternalServerError, codeConfigInvalid, err)
			return
		}
		envelope(w, http.StatusOK, 0, settings, "")
	default:
		envelopeErr(w, http.StatusMethodNotAllowed, codeInvalidRequest, fmt.Errorf("method not allowed"))
	}
}

func (c *v2Controller) imageProfiles(w http.ResponseWriter, r *http.Request, rest string) {
	id := strings.Trim(strings.TrimPrefix(rest, "/"), "/")
	if id == "" {
		switch r.Method {
		case http.MethodGet:
			profiles, err := c.imageConfig.LoadProfiles()
			if err != nil {
				envelopeErr(w, http.StatusInternalServerError, codeConfigInvalid, err)
				return
			}
			envelope(w, http.StatusOK, 0, profiles, "")
		case http.MethodPost:
			var profile imagejob.Profile
			if err := decodeBody(r, &profile); err != nil {
				envelopeErr(w, http.StatusBadRequest, codeInvalidRequest, err)
				return
			}
			c.saveImageProfile(w, profile)
		default:
			envelopeErr(w, http.StatusMethodNotAllowed, codeInvalidRequest, fmt.Errorf("method not allowed"))
		}
		return
	}
	switch r.Method {
	case http.MethodGet:
		profile, err := c.imageConfig.LoadProfile(id)
		if err != nil {
			envelopeErr(w, http.StatusNotFound, codeNotFound, err)
			return
		}
		envelope(w, http.StatusOK, 0, profile, "")
	case http.MethodPut:
		var profile imagejob.Profile
		if err := decodeBody(r, &profile); err != nil {
			envelopeErr(w, http.StatusBadRequest, codeInvalidRequest, err)
			return
		}
		profile.ID = id
		c.saveImageProfile(w, profile)
	case http.MethodDelete:
		settings, _ := c.imageConfig.LoadSettings()
		if settings.Novel.DefaultProfileID == id || settings.Chat.DefaultProfileID == id || settings.Play.DefaultProfileID == id {
			envelopeErr(w, http.StatusConflict, codeConflict, fmt.Errorf("profile is used by scene settings"))
			return
		}
		if err := c.imageConfig.DeleteProfile(id); err != nil {
			envelopeErr(w, http.StatusInternalServerError, codeConfigInvalid, err)
			return
		}
		envelope(w, http.StatusOK, 0, map[string]bool{"deleted": true}, "")
	default:
		envelopeErr(w, http.StatusMethodNotAllowed, codeInvalidRequest, fmt.Errorf("method not allowed"))
	}
}

func (c *v2Controller) saveImageProfile(w http.ResponseWriter, profile imagejob.Profile) {
	if !c.requireHost(w) {
		return
	}
	profile = imagejob.NormalizeProfile(profile)
	if err := c.svc.ValidateProfile(profile); err != nil {
		envelopeErr(w, http.StatusUnprocessableEntity, codeConfigInvalid, err)
		return
	}
	if err := c.imageConfig.SaveProfile(profile); err != nil {
		envelopeErr(w, http.StatusInternalServerError, codeConfigInvalid, err)
		return
	}
	envelope(w, http.StatusOK, 0, profile, "")
}

func (c *v2Controller) imageProviders(w http.ResponseWriter, r *http.Request, rest string) {
	rest = strings.Trim(rest, "/")
	if rest == "" {
		if r.Method != http.MethodGet {
			envelopeErr(w, http.StatusMethodNotAllowed, codeInvalidRequest, fmt.Errorf("method not allowed"))
			return
		}
		envelope(w, http.StatusOK, 0, c.svc.Providers(), "")
		return
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[1] != "test" || r.Method != http.MethodPost {
		envelopeErr(w, http.StatusNotFound, codeNotFound, fmt.Errorf("route not found"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	err := c.svc.TestProvider(ctx, parts[0])
	cancel()
	if err != nil {
		envelopeErr(w, http.StatusBadGateway, codeUnreachable, err)
		return
	}
	envelope(w, http.StatusOK, 0, map[string]bool{"ok": true}, "")
}

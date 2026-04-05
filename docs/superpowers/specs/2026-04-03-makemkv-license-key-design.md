# MakeMKV License Key Support

**Date:** 2026-04-03  
**Status:** Approved

## Context

MakeMKV supports both a free beta/trial license and paid registration keys. BluForge already reads `MAKEMKV_KEY` at container startup and writes it to `~/.MakeMKV/settings.conf` — but this mechanism is undocumented and not editable at runtime. Users must set the env var and restart the container to change or add a key. This design adds runtime editability via the Settings UI and documents the env var properly.

## Design

### 1. Config & Persistence

Add `MakeMKVKey string` (yaml: `makemkv_key`) to `AppConfig` in `internal/config/config.go`. Add `MAKEMKV_KEY` env var reading to `LoadFromEnv()` so it participates in the standard precedence chain (hardcoded defaults → env vars → YAML file → runtime UI update).

Change `setupMakeMKVData()` in `main.go` to source the key from `cfg.MakeMKVKey` rather than calling `os.Getenv("MAKEMKV_KEY")` directly — keeping env var reading in one place (the config package).

To apply a new key immediately when saved via UI (no restart required), the `Server` needs a way to re-call `writeMakeMKVSettings`. The cleanest approach — consistent with how the SSE broadcast callback is wired — is to accept an `onMakeMKVKeyChange func(key string)` callback in `ServerDeps`. In `main.go` this callback writes the new key to `~/.MakeMKV/settings.conf` via `writeMakeMKVSettings`.

### 2. Settings UI

Add a "MakeMKV License" card to `templates/settings.templ`, placed between the General and Track Selection sections. It contains a single password input (`makemkv_key`) using the existing `maskSecret()` function. Helper text: "Enter your MakeMKV registration key. Applies immediately — no restart required. Leave blank to use the `MAKEMKV_KEY` environment variable, or omit entirely to run in free trial mode."

Add `MakeMKVKey string` to the `SettingsData` struct.

### 3. Handler Changes

**GET `/settings`** (`handleSettings`): populate `data.MakeMKVKey` from `cfg.MakeMKVKey`.

**POST `/settings`** (`handleSettingsSave`): read `makemkv_key` form value. If the value is `"••••••••"` (the masked placeholder), leave `cfg.MakeMKVKey` unchanged. Otherwise, set `cfg.MakeMKVKey` to the submitted value (empty string clears the key). Invoke the `onMakeMKVKeyChange` callback with the new key value after saving config.

### 4. Documentation

**`docker-compose.yml`** and **`docker-compose.unraid.yml`**: Add a commented-out example line in the `environment` block:
```yaml
# - MAKEMKV_KEY=your_key_here  # Optional: MakeMKV registration key. Free beta key available at https://www.makemkv.com/forum/viewtopic.php?t=1053
```

**`README.md`**: Add a row to the Configuration table for `MAKEMKV_KEY`:
- Setting: *(optional)*
- Env Var: `MAKEMKV_KEY`
- Default: *(none)*
- Description: MakeMKV registration key. Can also be set at runtime via the Settings page. Free beta key available at the MakeMKV forum.

## Files to Modify

| File | Change |
|------|--------|
| `internal/config/config.go` | Add `MakeMKVKey` field, env var loading |
| `main.go` | Pass key from `cfg.MakeMKVKey`; wire `onMakeMKVKeyChange` callback |
| `internal/web/server.go` | Add `OnMakeMKVKeyChange func(string)` to `ServerDeps` |
| `internal/web/handlers_settings.go` | Read/write `MakeMKVKey`; invoke callback on save |
| `templates/settings.templ` | Add `MakeMKVKey` to `SettingsData`; add UI card |
| `docker-compose.yml` | Add commented `MAKEMKV_KEY` example |
| `docker-compose.unraid.yml` | Add commented `MAKEMKV_KEY` example |
| `README.md` | Add `MAKEMKV_KEY` row to Configuration table |

## Verification

1. Build: `go build -o bluforge .` — no compile errors
2. Template generation: `templ generate` — no errors
3. Start the app; confirm settings page renders with the new MakeMKV License card
4. Enter a key, save — confirm `~/.MakeMKV/settings.conf` contains `app_Key = "..."` without restart
5. Reload settings page — confirm key is masked (`••••••••`), not shown in plaintext
6. Clear the key (submit empty) — confirm `MakeMKVKey` is cleared in config and `app_Key` is absent from `settings.conf`; submitting the masked placeholder `••••••••` leaves the existing key untouched
7. Set `MAKEMKV_KEY` env var, restart — confirm it seeds into the UI field correctly
8. Confirm `docker-compose.yml`, `docker-compose.unraid.yml`, and README all reference `MAKEMKV_KEY`

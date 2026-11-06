#!/bin/sh
set -eu

unset GEMINI_API_KEY GOOGLE_API_KEY GOOGLE_GEMINI_BASE_URL AGY_ADC_AUTH GOOGLE_APPLICATION_CREDENTIALS
umask 077
mkdir -p "$HOME/.gemini/antigravity-cli"
settings=$(mktemp "$HOME/.gemini/antigravity-cli/.settings.XXXXXX")
printf '%s\n' '{"useG1Credits":false}' > "$settings"
mv "$settings" "$HOME/.gemini/antigravity-cli/settings.json"

exec /usr/local/bin/agy "$@"

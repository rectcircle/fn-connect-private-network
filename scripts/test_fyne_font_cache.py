"""Test the actual pinned Fyne overlay after preparing the Android build."""
from pathlib import Path
import subprocess

ROOT=Path(__file__).resolve().parents[1]
local=ROOT/'apps/android/app/build/generated/fyne-mobile-v2.8.2'
modfile=ROOT/'apps/android/app/build/generated/android.mod'
test=local/'internal/painter/fncpn_parsed_font_cache_test.go'
if not modfile.exists() or not (local/'internal/painter/fncpn_parsed_font_cache.go').exists():
    raise SystemExit('Prepare the Android build first')
test.write_text((ROOT/'scripts/fyne/parsed_font_cache_test.go.in').read_text())
try:
    subprocess.run(['go','test','-race','-modfile',str(modfile),'-tags','ci',
                    'fyne.io/fyne/v2/internal/painter','-run','^TestFnCPN','-count=1'],cwd=ROOT/'apps/shared/fyne',check=True)
finally:
    test.unlink(missing_ok=True)

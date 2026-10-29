"""Run the keyboard/relayout regression against the actual local mobile driver."""
from pathlib import Path
import subprocess

ROOT=Path(__file__).resolve().parents[1]
local=ROOT/'apps/android/app/build/generated/fyne-mobile-v2.8.2'
modfile=ROOT/'apps/android/app/build/generated/android.mod'
test=local/'internal/driver/mobile/fncpn_keyboard_tap_test.go'
if not modfile.exists():raise SystemExit('Prepare the Android build first')
test.write_text((ROOT/'scripts/fyne/keyboard_tap_test.go.in').read_text())
try:
    subprocess.run(['go','test','-modfile',str(modfile),'-tags','ci',
                    'fyne.io/fyne/v2/internal/driver/mobile','-run','^TestFnCPNKeyboard','-count=1','-timeout=60s'],cwd=ROOT/'apps/shared/fyne',check=True)
finally:
    test.unlink(missing_ok=True)

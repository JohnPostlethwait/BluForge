package makemkv

import "testing"

func TestDriveIdentity(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"model, firmware and serial keeps model and serial", "BD-RE ASUS BW-16D1HT 3.10 KLFO82D3028", "BD-RE ASUS BW-16D1HT KLFO82D3028"},
		{"product with spaces", "BD-RE HL-DT-ST BD-RE  WH16NS40 1.02 SIK9TH8A1234", "BD-RE HL-DT-ST BD-RE WH16NS40 SIK9TH8A1234"},
		{"firmware without serial is dropped", "BD-RE ASUS BW-16D1HT 3.10", "BD-RE ASUS BW-16D1HT"},
		{"no firmware or serial", "BD-RE HL-DT-ST BD-RE  WH16NS40", "BD-RE HL-DT-ST BD-RE WH16NS40"},
		{"short name", "BD-RE PIONEER", "BD-RE PIONEER"},
		{"empty", "", ""},
		{"surrounding whitespace", "  BD-RE ASUS BW-16D1HT 3.10 KLFO82D3028 ", "BD-RE ASUS BW-16D1HT KLFO82D3028"},
		{"firmware not in dotted form is kept", "BD-RE LITEON DH-401S VL13 ABC123", "BD-RE LITEON DH-401S VL13 ABC123"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DriveIdentity(tt.in); got != tt.want {
				t.Errorf("DriveIdentity(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestDriveIdentitySurvivesFirmwareUpdate(t *testing.T) {
	before := DriveIdentity("BD-RE ASUS BW-16D1HT 3.02 KLFO82D3028")
	after := DriveIdentity("BD-RE ASUS BW-16D1HT 3.10 KLFO82D3028")
	if before != after {
		t.Errorf("identity changed across a firmware update: %q vs %q", before, after)
	}
}

func TestDriveIdentityTellsSameModelApartBySerial(t *testing.T) {
	a := DriveIdentity("BD-RE ASUS BW-16D1HT 3.10 KLFO82D3028")
	b := DriveIdentity("BD-RE ASUS BW-16D1HT 3.10 KLFO82D9999")
	if a == b {
		t.Errorf("two drives of the same model share identity %q", a)
	}
}

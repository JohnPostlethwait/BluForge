package drives

import "testing"

func TestReadinessFromTestUnitReady(t *testing.T) {
	fixed := func(key, asc, ascq byte) []byte {
		s := make([]byte, 18)
		s[0], s[2], s[12], s[13] = 0x70, key, asc, ascq
		return s
	}
	cases := []struct {
		name      string
		status    byte
		transport bool
		sense     []byte
		want      Readiness
	}{
		{"good status", 0, false, nil, Readiness{Ready: true}},
		{"spinning up", 0x02, false, fixed(0x02, 0x04, 0x01), Readiness{Reason: "NOT READY 04/01"}},
		{"just inserted", 0x02, false, fixed(0x06, 0x28, 0x00), Readiness{Reason: "UNIT ATTENTION 28/00"}},
		{"tray open", 0x02, false, fixed(0x02, 0x3a, 0x02), Readiness{Reason: "NOT READY 3A/02"}},
		{"descriptor sense", 0x02, false, []byte{0x72, 0x02, 0x04, 0x01}, Readiness{Reason: "NOT READY 04/01"}},
		{"no sense data", 0x02, false, nil, Readiness{Reason: "CHECK CONDITION without sense data"}},
		{"busy", 0x08, false, nil, Readiness{Reason: "SCSI status 0x08"}},
		{"transport failure", 0, true, nil, Readiness{Reason: "the command did not reach the drive"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := readinessFrom(c.status, c.transport, c.sense); got != c.want {
				t.Errorf("readinessFrom = %+v, want %+v", got, c.want)
			}
		})
	}
}

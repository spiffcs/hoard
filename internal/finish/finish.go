package finish

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
)

type Finish struct{ s string }

var (
	Nonfoil = Finish{"nonfoil"}
	Foil    = Finish{"foil"}
	Etched  = Finish{"etched"}
)

func All() []Finish { return []Finish{Nonfoil, Foil, Etched} }

func Parse(s string) (Finish, error) {
	for _, f := range All() {
		if f.s == s {
			return f, nil
		}
	}
	return Finish{}, fmt.Errorf("invalid finish %q", s)
}

func (f Finish) String() string { return f.s }

func (f Finish) UsesFoilPricing() bool { return f == Foil || f == Etched }

func (f Finish) EffectivePrice(usd, foil, etched *float64) *float64 {
	if !f.UsesFoilPricing() {
		return usd
	}
	if f == Etched && etched != nil {
		return etched
	}
	return foil
}

func (f Finish) Value() (driver.Value, error) {
	if _, err := Parse(f.s); err != nil {
		return nil, err
	}
	return f.s, nil
}

func (f *Finish) Scan(src any) error {
	var s string
	switch v := src.(type) {
	case string:
		s = v
	case []byte:
		s = string(v)
	default:
		return fmt.Errorf("cannot read a finish from %T", src)
	}
	parsed, err := Parse(s)
	if err != nil {
		return err
	}
	*f = parsed
	return nil
}

func (f Finish) MarshalJSON() ([]byte, error) {
	if _, err := Parse(f.s); err != nil {
		return nil, err
	}
	return json.Marshal(f.s)
}

func (f *Finish) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("a finish must be a JSON string: %w", err)
	}
	parsed, err := Parse(s)
	if err != nil {
		return err
	}
	*f = parsed
	return nil
}

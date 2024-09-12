package pricing

import "strconv"

type InstancePricing struct {
	InstanceType string       `json:"instance_type"`
	VCpu         Float        `json:"vcpu"`
	Memory       Float        `json:"memory"`
	EDPPrice     PriceDetails `json:"edp_price"`
}

type PriceDetails struct {
	PayAsYouGo    Float `json:"pay_as_you_go"`
	Reserved1Year Float `json:"ri_1_year"`
	Reserved3Year Float `json:"ri_3_years"`
}

type AllInstancePricing struct {
	Results []InstancePricing `json:"results"`
}

type Float float64

func (f *Float) UnmarshalJSON(b []byte) error {
	if string(b) == "\"NA\"" || len(b) == 0 {
		*f = Float(0)
		return nil
	}
	val, err := strconv.ParseFloat(string(b), 64)
	if err != nil {
		return err
	}
	*f = Float(val)
	return nil
}

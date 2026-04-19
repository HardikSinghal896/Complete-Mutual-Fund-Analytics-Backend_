package model

type NAVEntry struct {
	Date string  `json:"date"`
	NAV  float64 `json:"nav"`
}

type Fund struct {
	Code string     `json:"code"`
	Name string     `json:"name"`
	NAVs []NAVEntry `json:"navs"`
}

func (f *Fund) LatestNAV() float64 {
	if len(f.NAVs) == 0 {
		return 0
	}
	return f.NAVs[0].NAV
}

func (f *Fund) OldestNAV() float64 {
	if len(f.NAVs) == 0 {
		return 0
	}
	return f.NAVs[len(f.NAVs)-1].NAV
}

func (f *Fund) SimpleReturn() float64 {
	oldest := f.OldestNAV()
	if oldest == 0 {
		return 0
	}
	return (f.LatestNAV() - oldest) / oldest * 100
}
package constant

const (
	StrategyRace     = "race"
	StrategyFallback = "fallback"
)

var knownStrategies = []string{
	StrategyRace,
	StrategyFallback,
}

func KnownStrategies() []string {
	return append([]string(nil), knownStrategies...)
}
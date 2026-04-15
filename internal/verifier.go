package internal

func VerifyAlgorithm(algo string) bool {
	switch algo {
	case "fixed_window":
		return true
	default:
		return false
	}
}

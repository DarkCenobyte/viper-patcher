package creatorgui

func fittedFilePairTableHeight(preferred, minimum, desiredWindowHeight, maximumWindowHeight float32) float32 {
	if desiredWindowHeight <= maximumWindowHeight {
		return preferred
	}
	height := preferred - (desiredWindowHeight - maximumWindowHeight)
	if height < minimum {
		return minimum
	}
	if height > preferred {
		return preferred
	}
	return height
}

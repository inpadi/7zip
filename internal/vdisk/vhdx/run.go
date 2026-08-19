package vhdx

import "errors"

type PartialRun struct {
	Type  byte
	Count int64
}

func partialRunIter(bitmap []byte, startIdx int64, length int64, cb func(*PartialRun) error) error {
	if len(bitmap) == 0 || startIdx < 0 || startIdx >= 8 || length < 0 || length > int64(len(bitmap))*8-startIdx {
		return errors.New("invalid VHDX sector bitmap range")
	}
	if length == 0 {
		return nil
	}
	currentType := (bitmap[0] & (1 << startIdx)) >> startIdx
	currentCount := int64(0)

	for _, byteVal := range bitmap {
		if (currentType == 0 && byteVal == 0) || (currentType == 1 && byteVal == 0xFF) {
			maxCount := min64(length, int64(8-startIdx))
			currentCount += maxCount
			length -= maxCount
			startIdx = 0
		} else {
			end := min64(8, startIdx+length)
			for bitIdx := startIdx; bitIdx < end; bitIdx++ {
				sectorType := (byteVal & (1 << bitIdx)) >> bitIdx

				if sectorType == currentType {
					currentCount++
				} else {
					if err := cb(&PartialRun{Type: currentType, Count: currentCount}); err != nil {
						return err
					}
					currentType = sectorType
					currentCount = 1
				}
				length--
			}
			startIdx = 0
		}
		if length == 0 {
			break
		}
	}

	if currentCount > 0 {
		return cb(&PartialRun{Type: currentType, Count: currentCount})
	}

	return nil
}

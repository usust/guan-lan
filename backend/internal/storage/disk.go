package storage

import "fmt"

// CheckDiskBudget leaves reserve bytes available after a proposed write.
// This is admission control, not a filesystem quota or a reservation.
func CheckDiskBudget(path string, required, reserve uint64) error {
	available, err := AvailableBytes(path)
	if err != nil {
		return fmt.Errorf("probe storage capacity: %w", err)
	}
	return checkBudget(available, required, reserve)
}

func checkBudget(available, required, reserve uint64) error {
	// Subtraction avoids overflow when required+reserve exceeds uint64.
	if available < reserve || required > available-reserve {
		return fmt.Errorf("DISK_LOW: available=%d required=%d reserve=%d", available, required, reserve)
	}
	return nil
}

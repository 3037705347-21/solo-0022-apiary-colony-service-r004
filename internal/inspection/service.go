package inspection

import (
	"time"

	"example.com/apiary-colony-service/internal/domain"
	"example.com/apiary-colony-service/internal/store"
)

// Service 接收巡检、完成范围校验并派生关注项。
type Service struct {
	store *store.Memory
	clock func() time.Time
}

func New(memory *store.Memory, clock func() time.Time) *Service {
	if clock == nil {
		clock = time.Now
	}
	return &Service{store: memory, clock: clock}
}

type RecordInput struct {
	ObservedAt     time.Time
	ColonyStrength int
	BroodPattern   string
	Temper         string
	MiteCount      int
	Note           string
}

// Record 先完成入参校验与关注项派生，再交由仓储在写锁内做状态校验并落盘。
// 关注项随巡检记录一起固化，蜂场汇总在读取时以每箱最新巡检为准重新推导，
// 因此被拒绝的请求不会留下任何痕迹，恢复正常的巡检也会立即解除关注。
func (s *Service) Record(hiveID string, input RecordInput) (domain.Inspection, error) {
	observedAt := input.ObservedAt
	if observedAt.IsZero() {
		observedAt = s.clock()
	}
	record, err := domain.NewInspection(
		hiveID,
		observedAt,
		input.ColonyStrength,
		domain.BroodPattern(domain.TrimText(input.BroodPattern)),
		domain.Temper(domain.TrimText(input.Temper)),
		input.MiteCount,
		input.Note,
	)
	if err != nil {
		return domain.Inspection{}, err
	}
	record.Concerns = DeriveConcerns(record)
	return s.store.AddInspection(hiveID, record)
}

func (s *Service) List(hiveID string) ([]domain.Inspection, error) {
	return s.store.ListInspections(hiveID)
}

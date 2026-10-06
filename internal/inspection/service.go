package inspection

import (
	"sort"
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
	s.publishConcerns(hiveID, record.Concerns)
	saved, err := s.store.AddInspection(hiveID, record)
	if err != nil {
		return domain.Inspection{}, err
	}
	return saved, nil
}

// publishConcerns 把这次巡检的关注结果登记到蜂场汇总读取模型。
func (s *Service) publishConcerns(hiveID string, concerns []string) {
	if len(concerns) == 0 {
		return
	}
	hive, ok := s.store.GetHive(hiveID)
	if !ok {
		return
	}
	index := s.store.ApiaryIndex(hive.ApiaryID)
	for _, existing := range index.ConcernHives {
		if existing == hiveID {
			return
		}
	}
	index.ConcernHives = append(index.ConcernHives, hiveID)
	sort.Strings(index.ConcernHives)
	s.store.PutApiaryIndex(hive.ApiaryID, index)
}

func (s *Service) List(hiveID string) ([]domain.Inspection, error) {
	return s.store.ListInspections(hiveID)
}

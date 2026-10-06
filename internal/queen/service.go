package queen

import (
	"time"

	"example.com/apiary-colony-service/internal/domain"
	"example.com/apiary-colony-service/internal/store"
)

const (
	ReasonAging      = "aging"
	ReasonAggression = "aggression"
	ReasonLoss       = "loss"
)

// Service 处理换王：关闭旧蜂王记录并写入带标记色的新记录。
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

type ReplaceInput struct {
	Origin       string
	IntroducedAt time.Time
	Reason       string
}

func (s *Service) Replace(hiveID string, input ReplaceInput) (domain.Queen, error) {
	origin, err := ParseOrigin(input.Origin)
	if err != nil {
		return domain.Queen{}, err
	}
	reason, err := ParseReason(input.Reason)
	if err != nil {
		return domain.Queen{}, err
	}
	introducedAt := input.IntroducedAt
	if introducedAt.IsZero() {
		introducedAt = s.clock()
	}
	record, err := domain.NewQueen(hiveID, introducedAt, origin, ColorForIntroduction(introducedAt))
	if err != nil {
		return domain.Queen{}, err
	}
	closeState := domain.QueenSuperseded
	if reason == ReasonLoss {
		closeState = domain.QueenLost
	}
	// 蜂箱存在性、退役状态与待关闭蜂王都由仓储在同一把写锁内解析与判定，
	// 因此并发换王只会有一个请求关闭原蜂王，且被拒绝的请求不改动任何记录。
	return s.store.AddQueen(hiveID, record, closeState, s.clock())
}

func (s *Service) List(hiveID string) ([]domain.Queen, error) {
	return s.store.ListQueens(hiveID)
}

func ParseOrigin(value string) (domain.QueenOrigin, error) {
	origin := domain.QueenOrigin(domain.TrimText(value))
	if !domain.ValidQueenOrigin(origin) {
		return "", domain.Invalid(
			"origin",
			"origin 必须是 %s、%s 或 %s",
			domain.OriginBred, domain.OriginCells, domain.OriginSwarm,
		)
	}
	return origin, nil
}

func ParseReason(value string) (string, error) {
	reason := domain.TrimText(value)
	switch reason {
	case ReasonAging, ReasonAggression, ReasonLoss:
		return reason, nil
	default:
		return "", domain.Invalid("reason", "reason 必须是 %s、%s 或 %s", ReasonAging, ReasonAggression, ReasonLoss)
	}
}

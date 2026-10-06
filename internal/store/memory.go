package store

import (
	"fmt"
	"sync"
	"time"

	"example.com/apiary-colony-service/internal/domain"
)

// Memory 是并发安全的内存仓储。所有写入都在写锁内先完成状态校验再落盘，
// 校验失败时不改动任何数据；读取则通过 Snapshot 得到一致视图。
type Memory struct {
	mu sync.RWMutex

	apiaries    map[string]domain.Apiary
	hives       map[string]domain.Hive
	inspections map[string][]domain.Inspection
	queens      map[string][]domain.Queen

	inspectionSeq int
	queenSeq      int
}

func NewMemory() *Memory {
	return &Memory{
		apiaries:    make(map[string]domain.Apiary),
		hives:       make(map[string]domain.Hive),
		inspections: make(map[string][]domain.Inspection),
		queens:      make(map[string][]domain.Queen),
	}
}

func (m *Memory) CreateApiary(apiary domain.Apiary) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.apiaries[apiary.ID]; ok {
		return domain.Conflict("蜂场 %q 已存在", apiary.ID)
	}
	m.apiaries[apiary.ID] = apiary
	return nil
}

func (m *Memory) GetApiary(id string) (domain.Apiary, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	apiary, ok := m.apiaries[id]
	return apiary, ok
}

func (m *Memory) CreateHive(hive domain.Hive) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.apiaries[hive.ApiaryID]; !ok {
		return domain.NotFound("蜂场", hive.ApiaryID)
	}
	if _, ok := m.hives[hive.ID]; ok {
		return domain.Conflict("蜂箱 %q 已存在", hive.ID)
	}
	m.hives[hive.ID] = hive
	return nil
}

func (m *Memory) GetHive(id string) (domain.Hive, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	hive, ok := m.hives[id]
	return hive, ok
}

// AdvanceHive 在同一把写锁内完成状态读取、迁移校验与写入，避免并发迁移产生交错结果。
func (m *Memory) AdvanceHive(hiveID string, target domain.HiveState, _ time.Time) (domain.Transition, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	hive, ok := m.hives[hiveID]
	if !ok {
		return domain.Transition{}, domain.NotFound("蜂箱", hiveID)
	}
	if hive.State == target {
		return domain.Transition{}, domain.InvalidState("蜂箱 %q 已经处于状态 %q", hiveID, target)
	}
	if !domain.CanTransitionHive(hive.State, target) {
		return domain.Transition{}, domain.InvalidState(
			"蜂箱 %q 不允许从 %q 迁移到 %q", hiveID, hive.State, target)
	}
	from := hive.State
	hive.State = target
	m.hives[hiveID] = hive
	return domain.Transition{HiveID: hiveID, From: from, To: target}, nil
}

// AddInspection 先在写锁内确认蜂箱存在且仍接受巡检，再分配编号、写入记录并
// 推进蜂箱的最近巡检时间。任何校验失败都在第一次写入之前返回，仓储不留痕迹；
// 锁同时串行化退役与巡检、巡检与巡检，避免并发交错产生丢失或重复。
func (m *Memory) AddInspection(hiveID string, inspection domain.Inspection) (domain.Inspection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	hive, ok := m.hives[hiveID]
	if !ok {
		return domain.Inspection{}, domain.NotFound("蜂箱", hiveID)
	}
	if !hive.AcceptsInspection() {
		return domain.Inspection{}, domain.InvalidState("蜂箱 %q 处于 %q 状态，不接受巡检", hiveID, hive.State)
	}
	m.inspectionSeq++
	inspection.ID = fmt.Sprintf("inspection-%06d", m.inspectionSeq)
	inspection.HiveID = hiveID
	m.inspections[hiveID] = append(m.inspections[hiveID], inspection)

	observedAt := inspection.ObservedAt
	hive.LastInspectionAt = &observedAt
	m.hives[hiveID] = hive
	return inspection, nil
}

// AddQueen 在同一把写锁内先确认蜂箱存在且仍接受换王，再关闭当前产卵蜂王、
// 建立新蜂王并切换蜂箱的当前蜂王。待关闭的蜂王在锁内解析，保证并发换王只有
// 一个请求能关闭原蜂王，任意时刻同一蜂箱至多只有一只 laying 蜂王。
// 校验失败时不关闭旧记录、不追加新记录、不动蜂箱，因此被拒绝的请求不留痕迹。
func (m *Memory) AddQueen(
	hiveID string,
	queen domain.Queen,
	closeState domain.QueenState,
	now time.Time,
) (domain.Queen, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	hive, ok := m.hives[hiveID]
	if !ok {
		return domain.Queen{}, domain.NotFound("蜂箱", hiveID)
	}
	if !hive.AcceptsQueenChange() {
		return domain.Queen{}, domain.InvalidState("蜂箱 %q 处于 %q 状态，不接受换王", hiveID, hive.State)
	}
	if hive.ActiveQueenID != "" {
		items := m.queens[hiveID]
		closed := false
		for index, item := range items {
			if item.ID != hive.ActiveQueenID {
				continue
			}
			if !item.IsLaying() {
				return domain.Queen{}, domain.Conflict("蜂王 %q 已经关闭", item.ID)
			}
			closedAt := now.UTC()
			item.State = closeState
			item.ClosedAt = &closedAt
			items[index] = item
			closed = true
			break
		}
		if !closed {
			return domain.Queen{}, domain.Storage("蜂箱 %q 上的蜂王 %q 记录缺失", hiveID, hive.ActiveQueenID)
		}
	}
	m.queenSeq++
	queen.ID = fmt.Sprintf("queen-%06d", m.queenSeq)
	queen.HiveID = hiveID
	queen.State = domain.QueenLaying
	m.queens[hiveID] = append(m.queens[hiveID], queen)

	hive.ActiveQueenID = queen.ID
	m.hives[hiveID] = hive
	return queen, nil
}

func (m *Memory) ListInspections(hiveID string) ([]domain.Inspection, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, ok := m.hives[hiveID]; !ok {
		return nil, domain.NotFound("蜂箱", hiveID)
	}
	items := m.inspections[hiveID]
	out := make([]domain.Inspection, len(items))
	copy(out, items)
	return out, nil
}

func (m *Memory) ListQueens(hiveID string) ([]domain.Queen, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, ok := m.hives[hiveID]; !ok {
		return nil, domain.NotFound("蜂箱", hiveID)
	}
	items := m.queens[hiveID]
	out := make([]domain.Queen, len(items))
	copy(out, items)
	return out, nil
}

// Snapshot 返回深拷贝的一致视图，供汇总类读取使用。
func (m *Memory) Snapshot() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	snapshot := Snapshot{
		Apiaries:          make(map[string]domain.Apiary, len(m.apiaries)),
		Hives:             make(map[string]domain.Hive, len(m.hives)),
		InspectionsByHive: make(map[string][]domain.Inspection, len(m.inspections)),
		QueensByHive:      make(map[string][]domain.Queen, len(m.queens)),
	}
	for id, apiary := range m.apiaries {
		snapshot.Apiaries[id] = apiary
	}
	for id, hive := range m.hives {
		snapshot.Hives[id] = cloneHive(hive)
	}
	for id, items := range m.inspections {
		copied := make([]domain.Inspection, len(items))
		copy(copied, items)
		for index := range copied {
			copied[index].Concerns = append([]string{}, items[index].Concerns...)
		}
		snapshot.InspectionsByHive[id] = copied
	}
	for id, items := range m.queens {
		copied := make([]domain.Queen, len(items))
		copy(copied, items)
		for index := range copied {
			if items[index].ClosedAt != nil {
				closedAt := *items[index].ClosedAt
				copied[index].ClosedAt = &closedAt
			}
		}
		snapshot.QueensByHive[id] = copied
	}
	return snapshot
}

func cloneHive(hive domain.Hive) domain.Hive {
	if hive.LastInspectionAt != nil {
		observedAt := *hive.LastInspectionAt
		hive.LastInspectionAt = &observedAt
	}
	return hive
}

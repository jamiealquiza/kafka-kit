package kafkazk

import (
	"sort"
	"testing"
)

func TestChanges(t *testing.T) {
	b := BrokerStatus{}

	if b.Changes() {
		t.Errorf("Expected return 'false'")
	}

	b.New = 1
	if !b.Changes() {
		t.Errorf("Expected return 'true'")
	}
	b.New = 0

	b.Missing = 1
	if !b.Changes() {
		t.Errorf("Expected return 'true'")
	}
	b.Missing = 0

	b.OldMissing = 1
	if !b.Changes() {
		t.Errorf("Expected return 'true'")
	}
	b.OldMissing = 0

	b.Replace = 1
	if !b.Changes() {
		t.Errorf("Expected return 'true'")
	}
}

func TestSortBrokerListByCount(t *testing.T) {
	b := newMockBrokerMap2()
	bl := b.filteredList()

	sort.Sort(brokersByCount(bl))

	expected := []int{1001, 1002, 1004, 1005, 1003, 1006, 1007}

	for i, br := range bl {
		if br.ID != expected[i] {
			t.Errorf("Unexpected sort results")
		}
	}
}

func TestSortBrokerListByStorage(t *testing.T) {
	b := newMockBrokerMap2()
	bl := b.filteredList()

	sort.Sort(brokersByStorage(bl))

	expected := []int{1004, 1005, 1006, 1007, 1003, 1002, 1001}

	for i, br := range bl {
		if br.ID != expected[i] {
			t.Errorf("Unexpected sort results")
		}
	}
}

func TestUpdate(t *testing.T) {
	zk := &Mock{}
	bmm, _ := zk.GetAllBrokerMeta(false)
	bm := newMockBrokerMap()
	// 1001 isn't in the list, should
	// add to the Missing count.
	delete(bmm, 1001)
	// 1002 will be in the list but
	// missing, should add to the
	// OldMissing count.
	delete(bmm, 1002)

	// 1006 doesn't exist in the meta map.
	// This should also add to the missing.
	stat := bm.Update([]int{1002, 1003, 1005, 1006}, bmm)

	if stat.New != 1 {
		t.Errorf("Expected New count of 1, got %d", stat.New)
	}
	if stat.Missing != 2 {
		t.Errorf("Expected Missing count of 2, got %d", stat.Missing)
	}
	if stat.OldMissing != 1 {
		t.Errorf("Expected OldMissing count of 1, got %d", stat.OldMissing)
	}
	if stat.Replace != 2 {
		t.Errorf("Expected Replace count of 2, got %d", stat.Replace)
	}

	// Ensure all broker IDs are in the map.
	for _, id := range []int{0, 1001, 1002, 1003, 1004, 1005} {
		if _, ok := bm[id]; !ok {
			t.Errorf("Expected presence of ID %d", id)
		}
	}

	// Test that brokers have appropriately
	// updated fields.

	if !bm[1001].Missing {
		t.Errorf("Expected ID 1001 Missing == true")
	}
	if !bm[1001].Replace {
		t.Errorf("Expected ID 1001 Replace == true")
	}

	if !bm[1002].Missing {
		t.Errorf("Expected ID 1002 Missing == true")
	}
	if !bm[1002].Replace {
		t.Errorf("Expected ID 1002 Replace == true")
	}

	if bm[1003].Missing || bm[1003].Replace || bm[1003].New {
		t.Errorf("Unexpected fields set for ID 1003")
	}

	if bm[1004].Missing {
		t.Errorf("Expected ID 1004 Missing != true")
	}
	if !bm[1004].Replace {
		t.Errorf("Expected ID 1004 Replace == true")
	}

	if bm[1005].Missing || bm[1005].Replace {
		t.Errorf("Unexpected fields set for ID 1005")
	}
	if !bm[1005].New {
		t.Errorf("Expected ID 1005 New == true")
	}

	if _, exists := bm[1006]; exists {
		t.Errorf("ID 1006 unexpectedly exists in BrokerMap")
	}
}

func TestSubStorageAll(t *testing.T) {
	bm := newMockBrokerMap()
	pm, _ := PartitionMapFromString(testGetMapString("test_topic"))
	pmm := NewPartitionMetaMap()

	pmm["test_topic"] = map[int]*PartitionMeta{
		0: &PartitionMeta{Size: 30},
		1: &PartitionMeta{Size: 35},
		2: &PartitionMeta{Size: 60},
		3: &PartitionMeta{Size: 45},
	}

	err := bm.SubStorageAll(pm, pmm)
	if err != nil {
		t.Errorf("Unexpected error: %s", err)
	}

	expected := map[int]float64{
		1001: 225,
		1002: 310,
		1003: 405,
		1004: 505,
	}

	for _, b := range bm {
		if b.StorageFree != expected[b.ID] {
			t.Errorf("Expected '%f' StorageFree for ID %d, got '%f'",
				expected[b.ID], b.ID, b.StorageFree)
		}
	}
}

// func TestSubStorageAll(t *testing.T) {} TODO

func TestFilteredList(t *testing.T) {
	bm := newMockBrokerMap()
	bm[1003].Replace = true

	nl := bm.filteredList()
	expected := map[int]struct{}{
		1001: struct{}{},
		1002: struct{}{},
		1004: struct{}{},
	}

	for _, b := range nl {
		if _, exist := expected[b.ID]; !exist {
			t.Errorf("Broker ID %d shouldn't exist", b.ID)
		}
	}
}

func TestBrokerMapFromPartitionMap(t *testing.T) {
	zk := &Mock{}
	bmm, _ := zk.GetAllBrokerMeta(false)
	pm, _ := PartitionMapFromString(testGetMapString("test_topic"))
	forceRebuild := false

	brokers := BrokerMapFromPartitionMap(pm, bmm, forceRebuild)
	expected := newMockBrokerMap()

	for id, b := range brokers {
		switch {
		case b.ID != expected[id].ID:
			t.Errorf("Expected id %d, got %d for broker %d",
				expected[id].ID, b.ID, id)
		case b.Locality != expected[id].Locality:
			t.Errorf("Expected locality %s, got %s for broker %d",
				expected[id].Locality, b.Locality, id)
		case b.Used != expected[id].Used:
			t.Errorf("Expected used %d, got %d for broker %d",
				expected[id].Used, b.Used, id)
		case b.Replace != expected[id].Replace:
			t.Errorf("Expected replace %t, got %t for broker %d",
				expected[id].Replace, b.Replace, id)
		}
	}
}

// func TestMappedBrokers(t *Testing.T) // TODO
// func TestNonReplacedBrokers(t *Testing.T) // TODO

func TestBrokerMapCopy(t *testing.T) {
	bm1 := newMockBrokerMap()
	bm2 := bm1.Copy()

	if len(bm1) != len(bm2) {
		t.Errorf("Unexpected length inequality")
	}

	for b := range bm1 {
		switch {
		case bm1[b].ID != bm2[b].ID:
			t.Errorf("id field mismatch")
		case bm1[b].Locality != bm2[b].Locality:
			t.Errorf("locality field mismatch")
		case bm1[b].Used != bm2[b].Used:
			t.Errorf("used field mismatch")
		case bm1[b].Replace != bm2[b].Replace:
			t.Errorf("replace field mismatch")
		case bm1[b].StorageFree != bm2[b].StorageFree:
			t.Errorf("StorageFree field mismatch")
		}
	}
}

func TestSortPseudoShuffle(t *testing.T) {
	bl := newMockBrokerMap2().filteredList()

	// Test with seed val of 1.
	expected := []int{1001, 1002, 1005, 1004, 1007, 1003, 1006}
	bl.SortPseudoShuffle(1)

	for i, b := range bl {
		if b.ID != expected[i] {
			t.Errorf("Expected broker %d, got %d", expected[i], b.ID)
		}
	}

	// Test with seed val of 3.
	expected = []int{1001, 1005, 1002, 1004, 1003, 1006, 1007}
	bl.SortPseudoShuffle(3)

	for i, b := range bl {
		if b.ID != expected[i] {
			t.Errorf("Expected broker %d, got %d", expected[i], b.ID)
		}
	}
}

func TestBrokerStringToSlice(t *testing.T) {
	bs := BrokerStringToSlice("1001,1002,1003,1003")
	expected := []int{1001, 1002, 1003}

	if len(bs) != 3 {
		t.Errorf("Expected slice len of 3, got %d", len(bs))
	}

	for i, b := range bs {
		if b != expected[i] {
			t.Errorf("Expected ID %d, got %d", expected[i], b)
		}
	}
}

func newMockBrokerMap() BrokerMap {
	return BrokerMap{
		0:    &Broker{ID: 0, Replace: true},
		1001: &Broker{ID: 1001, Locality: "a", Used: 3, Replace: false, StorageFree: 100.00},
		1002: &Broker{ID: 1002, Locality: "b", Used: 3, Replace: false, StorageFree: 200.00},
		1003: &Broker{ID: 1003, Locality: "c", Used: 2, Replace: false, StorageFree: 300.00},
		1004: &Broker{ID: 1004, Locality: "a", Used: 2, Replace: false, StorageFree: 400.00},
	}
}

func newMockBrokerMap2() BrokerMap {
	return BrokerMap{
		0:    &Broker{ID: 0, Replace: true},
		1001: &Broker{ID: 1001, Locality: "a", Used: 2, Replace: false, StorageFree: 100.00},
		1002: &Broker{ID: 1002, Locality: "b", Used: 2, Replace: false, StorageFree: 200.00},
		1003: &Broker{ID: 1003, Locality: "c", Used: 3, Replace: false, StorageFree: 300.00},
		1004: &Broker{ID: 1004, Locality: "a", Used: 2, Replace: false, StorageFree: 400.00},
		1005: &Broker{ID: 1005, Locality: "b", Used: 2, Replace: false, StorageFree: 400.00},
		1006: &Broker{ID: 1006, Locality: "c", Used: 3, Replace: false, StorageFree: 400.00},
		1007: &Broker{ID: 1007, Locality: "a", Used: 3, Replace: false, StorageFree: 400.00},
	}
}

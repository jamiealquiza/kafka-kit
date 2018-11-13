package commands

import (
	"fmt"
	"math"
	"os"
	"sort"

	"github.com/DataDog/kafka-kit/kafkazk"

	"github.com/spf13/cobra"
)

func absDistance(x, t float64) float64 {
	return math.Abs(t-x) / t
}

type relocation struct {
	partition   kafkazk.Partition
	destination int
}

type planRelocationsForBrokerParams struct {
	sourceID           int
	relos              map[int][]relocation
	mappings           kafkazk.Mappings
	brokers            kafkazk.BrokerMap
	partitionMeta      kafkazk.PartitionMetaMap
	plan               relocationPlan
	pass               int
	topPartitionsLimit int
}

// relocationPlan is a mapping of topic,
// partition to a [2]int describing the
// source and destination broker to relocate
// a partition to and from.
type relocationPlan map[string]map[int][2]int

// add takes a kafkazk.Partition and a [2]int pair of
// source and destination broker IDs which the partition
// is scheduled to relocate from and to.
func (r relocationPlan) add(p kafkazk.Partition, ids [2]int) {
	if _, exist := r[p.Topic]; !exist {
		r[p.Topic] = make(map[int][2]int)
	}

	r[p.Topic][p.Partition] = ids
}

// isPlanned takes a kafkazk.Partition and returns whether
// a relocation is planned for the partition, along with
// the [2]int pair of source and destination broker IDs.
func (r relocationPlan) isPlanned(p kafkazk.Partition) (bool, [2]int) {
	var pair [2]int
	if _, exist := r[p.Topic]; !exist {
		return false, pair
	}

	if _, exist := r[p.Topic][p.Partition]; !exist {
		return false, pair
	} else {
		pair = r[p.Topic][p.Partition]
	}

	return true, pair
}

func validateBrokersForRebalance(cmd *cobra.Command, brokers kafkazk.BrokerMap, bm kafkazk.BrokerMetaMap) []int {
	// No broker changes are permitted in rebalance
	// other than new broker additions.
	fmt.Println("\nValidating broker list:")

	// Update the current BrokerList with
	// the provided broker list.
	c := brokers.Update(Config.brokers, bm)

	if c.Changes() {
		fmt.Printf("%s-\n", indent)
	}

	switch {
	case c.Missing > 0, c.OldMissing > 0, c.Replace > 0:
		fmt.Printf("%s[ERROR] rebalance only allows broker additions\n", indent)
		os.Exit(1)
	case c.New > 0:
		fmt.Printf("%s%d additional brokers added\n", indent, c.New)
		fmt.Printf("%s-\n", indent)
		fallthrough
	default:
		fmt.Printf("%sOK\n", indent)
	}

	// Find brokers where the storage free is t %
	// below the harmonic mean. Specifying 0 targets
	// all brokers.
	var offloadTargets []int
	thresh, _ := cmd.Flags().GetFloat64("storage-threshold")

	switch thresh {
	case 0.00:
		for _, b := range brokers {
			if !b.New && b.ID != 0 {
				offloadTargets = append(offloadTargets, b.ID)
			}
			sort.Ints(offloadTargets)
		}
	default:
		offloadTargets = brokers.BelowMean(thresh, brokers.HMean)
	}

	// Print rebalance parameters as a result of
	// input configurations and brokers found
	// to be beyond the storage threshold.
	fmt.Println("\nRebalance parameters:")

	tol, _ := cmd.Flags().GetFloat64("tolerance")
	mean, hMean := brokers.Mean(), brokers.HMean()

	fmt.Printf("%sFree storage mean, harmonic mean: %.2fGB, %.2fGB\n",
		indent, mean/div, hMean/div)

	fmt.Printf("%sBroker free storage limits (with a %.2f%% tolerance from mean):\n",
		indent, tol*100)

	fmt.Printf("%s%sSources limited to <= %.2fGB\n", indent, indent, mean*(1+tol)/div)
	fmt.Printf("%s%sDestinations limited to >= %.2fGB\n", indent, indent, mean*(1-tol)/div)

	fmt.Printf("\nBrokers targeted for partition offloading (>= %.2f%% threshold below hmean):\n", thresh*100)

	// Exit if no target brokers were found.
	if len(offloadTargets) == 0 {
		fmt.Printf("%s[none]\n", indent)
		os.Exit(0)
	} else {
		for _, id := range offloadTargets {
			fmt.Printf("%s%d\n", indent, id)
		}
	}

	return offloadTargets
}

func planRelocationsForBroker(cmd *cobra.Command, params planRelocationsForBrokerParams) int {
	verbose, _ := cmd.Flags().GetBool("verbose")
	tolerance, _ := cmd.Flags().GetFloat64("tolerance")
	localityScoped, _ := cmd.Flags().GetBool("locality-scoped")

	relos := params.relos
	mappings := params.mappings
	brokers := params.brokers
	partitionMeta := params.partitionMeta
	plan := params.plan
	sourceID := params.sourceID
	topPartitionsLimit := params.topPartitionsLimit

	// Use the arithmetic mean for target
	// thresholds.
	meanStorageFree := brokers.Mean()

	// Get the top partitions for the target broker.
	topPartn, _ := mappings.LargestPartitions(sourceID, topPartitionsLimit, partitionMeta)

	if verbose {
		fmt.Printf("\n[pass %d] Broker %d has a storage free of %.2fGB. Top partitions:\n",
			params.pass, sourceID, brokers[sourceID].StorageFree/div)

		for _, p := range topPartn {
			pSize, _ := partitionMeta.Size(p)
			fmt.Printf("%s%s p%d: %.2fGB\n",
				indent, p.Topic, p.Partition, pSize/div)
		}
	}

	targetLocality := brokers[sourceID].Locality

	// Plan partition movements. Each time a partition is planned
	// to be moved, it's unmapped from the broker so that it's
	// not retried the next iteration.
	var reloCount int
	for _, p := range topPartn {
		// Get a storage sorted brokerList.
		brokerList := brokers.List()
		brokerList.SortByStorage()

		pSize, _ := partitionMeta.Size(p)

		// Find a destination broker.
		var dest *kafkazk.Broker

		// Whether or not the destination broker should have the same
		// rack.id as the target. If so, choose the lowest utilized broker
		// in same locality. If not, choose the lowest utilized broker
		// the satisfies placement constraints considering the brokers
		// in the replica list (excluding the sourceID broker since it
		// would be replaced by the destination).
		switch localityScoped {
		case true:
			for _, b := range brokerList {
				if b.Locality == targetLocality && b.ID != sourceID {
					dest = b
					break
				}
			}
		case false:
			// Get constraints for all brokers in the
			// partition replica set, excluding the
			// sourceID broker.
			replicaSet := kafkazk.BrokerList{}
			for _, id := range p.Replicas {
				if id != sourceID {
					replicaSet = append(replicaSet, brokers[id])
				}
			}

			c := kafkazk.MergeConstraints(replicaSet)

			// Select the best candidate by storage.
			dest, _ = brokerList.BestCandidate(c, "storage", 0)
		}

		if dest == nil {
			return reloCount
		}

		if verbose {
			fmt.Printf("%s-\n", indent)
			fmt.Printf("%sAttempting migration plan for %s p%d\n", indent, p.Topic, p.Partition)
			fmt.Printf("%sCandidate destination broker %d has a storage free of %.2fGB\n",
				indent, dest.ID, dest.StorageFree/div)
		}

		sourceFree := brokers[sourceID].StorageFree + pSize
		destFree := dest.StorageFree - pSize

		// If the estimated storage change pushes either the
		// target or destination beyond the threshold distance
		// from the mean, try the next partition.

		sLim := meanStorageFree * (1 + tolerance)
		if sourceFree > sLim {
			if verbose {
				fmt.Printf("%sCannot move partition from target: "+
					"expected storage free %.2fGB above tolerated threshold of %.2fGB\n",
					indent, destFree/div, sLim/div)
			}

			continue
		}

		dLim := meanStorageFree * (1 - tolerance)
		if destFree < dLim {
			if verbose {
				fmt.Printf("%sCannot move partition to candidate: "+
					"expected storage free %.2fGB below tolerated threshold of %.2fGB\n",
					indent, destFree/div, dLim/div)
			}

			continue
		}

		// Otherwise, schedule the relocation.

		relos[sourceID] = append(relos[sourceID], relocation{partition: p, destination: dest.ID})
		reloCount++

		// Add to plan.
		plan.add(p, [2]int{sourceID, dest.ID})

		// Update StorageFree values.
		brokers[sourceID].StorageFree = sourceFree
		brokers[dest.ID].StorageFree = destFree

		// Remove the partition as being mapped
		// to the source broker.
		mappings.Remove(sourceID, p)

		if verbose {
			fmt.Printf("%sPlanning relocation to candidate\n", indent)
		}

		// Break at the first placement.
		break
	}

	return reloCount
}

func applyRelocationPlan(cmd *cobra.Command, pm *kafkazk.PartitionMap, plan relocationPlan) {
	// Traverse the partition list.
	for _, partn := range pm.Partitions {
		// If a relocation is planned for the partition,
		// replace the source ID with the planned
		// destination ID.
		if planned, p := plan.isPlanned(partn); planned {
			for i, r := range partn.Replicas {
				if r == p[0] {
					partn.Replicas[i] = p[1]
				}
			}
		}
	}

	// Optimize leaders.
	if t, _ := cmd.Flags().GetBool("optimize-leaders"); t {
		pm.SimpleLeaderOptimization()
	}
}

func printPlannedRelocations(targets []int, relos map[int][]relocation, pmm kafkazk.PartitionMetaMap) {
	for _, id := range targets {
		fmt.Printf("\nBroker %d relocations planned:\n", id)

		if _, exist := relos[id]; !exist {
			fmt.Printf("%s[none]\n", indent)
			continue
		}

		for _, r := range relos[id] {
			pSize, _ := pmm.Size(r.partition)
			fmt.Printf("%s%s[%.2fGB] %s p%d -> %d\n",
				indent, indent, pSize/div, r.partition.Topic, r.partition.Partition, r.destination)
		}
	}
}

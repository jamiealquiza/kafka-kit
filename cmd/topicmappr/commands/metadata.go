package commands

import (
	"fmt"
	"os"
	"regexp"
	"time"

	"github.com/DataDog/kafka-kit/v3/kafkazk"

	"github.com/spf13/cobra"
)

// checkMetaAge checks the age of the stored partition and broker storage
// metrics data against the tolerated metrics age parameter.
func checkMetaAge(cmd *cobra.Command, zk kafkazk.Handler) {
	age, err := zk.MaxMetaAge()
	if err != nil {
		fmt.Printf("Error fetching metrics metadata: %s\n", err)
		os.Exit(1)
	}

	tol, _ := cmd.Flags().GetInt("metrics-age")

	if age > time.Duration(tol)*time.Minute {
		fmt.Printf("Metrics metadata is older than allowed: %s\n", age)
		os.Exit(1)
	}
}

// getBrokerMeta returns a map of brokers and broker metadata for those
// registered in ZooKeeper. Optionally, metrics metadata persisted in ZooKeeper
// (via an external mechanism*) can be merged into the metadata.
func getBrokerMeta(cmd *cobra.Command, zk kafkazk.Handler, m bool) kafkazk.BrokerMetaMap {
	brokerMeta, errs := zk.GetAllBrokerMeta(m)
	// If no data is returned, report and exit. Otherwise, it's possible that
	// complete data for a few brokers wasn't returned. We check in subsequent
	// steps as to whether any brokers that matter are missing metrics.
	if errs != nil && brokerMeta == nil {
		for _, e := range errs {
			fmt.Println(e)
		}
		os.Exit(1)
	}

	return brokerMeta
}

// ensureBrokerMetrics takes a map of reference brokers and a map of discovered
// broker metadata. Any non-missing brokers in the broker map must be present
// in the broker metadata map and have a non-true MetricsIncomplete value.
func ensureBrokerMetrics(cmd *cobra.Command, bm kafkazk.BrokerMap, bmm kafkazk.BrokerMetaMap) {
	var e bool
	for id, b := range bm {
		// Missing brokers won't be found in the brokerMeta.
		if !b.Missing && id != kafkazk.StubBrokerID && bmm[id].MetricsIncomplete {
			e = true
			fmt.Printf("Metrics not found for broker %d\n", id)
		}
	}

	if e {
		os.Exit(1)
	}
}

// getPartitionMeta returns a map of topic, partition metadata persisted in
// ZooKeeper (via an external mechanism*). This is primarily partition size
// metrics data used for the storage placement strategy.
func getPartitionMeta(cmd *cobra.Command, zk kafkazk.Handler) kafkazk.PartitionMetaMap {
	partitionMeta, err := zk.GetAllPartitionMeta()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	return partitionMeta
}

// stripPendingDeletes takes a partition map and zk handler. It looks up any
// topics in a pending delete state and removes them from the provided partition
// map, returning a list of topics removed.
func stripPendingDeletes(pm *kafkazk.PartitionMap, zk kafkazk.Handler) []string {
	// Get pending deletions.
	pd, err := zk.GetPendingDeletion()
	if err != nil {
		fmt.Println("Error fetching topics pending deletion")
	}

	if len(pd) == 0 {
		return []string{}
	}

	// Convert to a series of literal regex.
	var re []*regexp.Regexp
	for _, topic := range pd {
		r := regexp.MustCompile(fmt.Sprintf(`^%s$`, topic))
		re = append(re, r)
	}

	// Update the PartitionMap and return a list of removed topic names.
	return removeTopics(pm, re)
}

// removeTopics takes a PartitionMap and []*regexp.Regexp of topic name patters.
// Any topic names that match any provided pattern will be removed from the
// PartitionMap and a []string of topics that were found and removed is returned.
func removeTopics(pm *kafkazk.PartitionMap, r []*regexp.Regexp) []string {
	var removedNames []string

	if len(r) == 0 {
		return removedNames
	}

	// Create a new PartitionList, populate non-removed topics, substitute the
	// existing PartitionList in the PartitionMap.
	newPL := kafkazk.PartitionList{}

	// Track what's removed.
	removed := map[string]struct{}{}

	// Traverse the partition map.
	for _, p := range pm.Partitions {
		for i, re := range r {
			// If the topic matches any regex pattern, add it to the removed set.
			if re.MatchString(p.Topic) {
				removed[p.Topic] = struct{}{}
				break
			}

			// We've checked all patterns.
			if i == len(r)-1 {
				// Else, it wasn't marked for removal; add it to the new PartitionList.
				newPL = append(newPL, p)
			}
		}
	}

	pm.Partitions = newPL

	for t := range removed {
		removedNames = append(removedNames, t)
	}

	return removedNames
}

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"math"
	"math/rand"
	"os"
	"path"
	"strings"
	"time"

	"github.com/pkg/errors"

	shell "github.com/ipfs/go-ipfs-api"

	"github.com/liamsi/ipld-plugin-experiments/merkle-tree"
)

var binFormattingMap = map[int]string{
	2:   "%01b",
	4:   "%02b",
	8:   "%03b",
	16:  "%04b",
	32:  "%05b",
	64:  "%06b",
	128: "%07b",
	256: "%08b",
}

func main() {
	cidFile := flag.String("cids-file", "testfiles/cids.json", "File with the CIDs (tree roots) to sample paths for.")
	numLeaves := flag.Int("num-leaves", 32, "Number of leaves. Will be used to determine the paths to sample.")
	numSamples := flag.Int("num-samples", 15, "Number of samples per block/tree. Each sample will run in a go-routine.")
	outDir := flag.String("out-dir", "ipfs-experiment-results", "Directory to save measurements to.")

	flag.Parse()

	if _, ok := binFormattingMap[*numLeaves]; !ok {
		fmt.Fprintf(os.Stderr, "Invalid number of leaves. Should be a power of two <= 256.\nShutting down client...")
		os.Exit(1)
	}

	if _, err := os.Stat(*outDir); os.IsNotExist(err) {
		os.Mkdir(*outDir, os.ModePerm)
	}

	cids := make([]string, 0)
	cidsBytes, err := ioutil.ReadFile(*cidFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error while reading CIDs file: %s.\nShutting down client...", err)
		os.Exit(1)
	}
	err = json.Unmarshal(cidsBytes, &cids)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error while parsing CIDs file: %s.\nShutting down client...", err)
		os.Exit(1)
	}
	log.Println("Sleep some time before the first sample request ...")
	time.Sleep(180 * time.Second)
	log.Println(" ... and we are back. Starting sampling")

	sh := shell.NewLocalShell()
	if sh == nil {
		log.Println("ipfs is not running properly. Shutting down...")
		os.Exit(1)
	}
	daProofLatency := make([]time.Duration, len(cids))
	singleSamplesLatency := make([]time.Duration, *numSamples*len(cids))
	for cidIter, cid := range cids {
		seenPaths := map[string]struct{}{}
		resChan := make(chan Result, *numSamples)
		for sampleIter := 0; sampleIter < *numSamples; sampleIter++ {
			// if not used properly this will cause an endless loop
			// (e.g. numSamples > #paths in tree == 2^numLeaves)
			path := generateUntriedRandPath(cid, *numLeaves, seenPaths)
			seenPaths[path] = struct{}{}
			go sampleLeaf(path, sh, resChan)
		}

		beforeSamples := time.Now()
		for i := 0; i < *numSamples; i++ {
			select {
			case msg1 := <-resChan:
				if msg1.Err != nil {
					// If there was an error, we use the max allowed Duration instead.
					singleSamplesLatency[i*(cidIter+1)] = math.MaxInt64
				} else {
					singleSamplesLatency[i*(cidIter+1)] = msg1.Elapsed
				}
				log.Println("received", msg1)
			}
		}
		elapsedDAProof := time.Since(beforeSamples)
		log.Printf("DA proof for cid %s took: %v\n", cid, elapsedDAProof)
		daProofLatency[cidIter] = elapsedDAProof
		fmt.Println("sleep in between rounds...")
		time.Sleep(30 * time.Second) // TODO make this configurable too
	}

	// Write results:
	sampleLatencyFile, err := os.Create(path.Join(*outDir, "sample_latencies.json"))
	defer sampleLatencyFile.Close()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error while creating file: %s.", err)
		os.Exit(1)
	}
	encoder := json.NewEncoder(sampleLatencyFile)
	err = encoder.Encode(&singleSamplesLatency)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error while writing latencies to file: %s.", err)
		os.Exit(1)
	}

	daProofLatencies, err := os.Create(path.Join(*outDir, "da_proof_latencies.json"))
	defer daProofLatencies.Close()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error while creating file: %s.", err)
		os.Exit(1)
	}
	encoder = json.NewEncoder(daProofLatencies)
	err = encoder.Encode(&daProofLatency)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error while writing latencies to file: %s.", err)
		os.Exit(1)
	}
}

func sampleLeaf(path string, sh *shell.Shell, resChan chan Result) (error, chan Result) {

	ln := &merkle.LeafNode{}
	log.Printf("Will request path: %s\n", path)

	now := time.Now()
	err := sh.DagGet(path, ln)
	if err != nil {
		log.Printf("Error while requesting %s from dag: %v", path, err)
		resChan <- Result{Err: errors.Wrap(err, fmt.Sprintf("could no get %s from dag", path))}
	} else {
		elapsed := time.Since(now)
		resChan <- Result{Elapsed: elapsed}

		log.Printf("DagGet %s took: %v\n", path, elapsed)
	}
	return err, resChan
}

func generateUntriedRandPath(cid string, numLeaves int, seenPaths map[string]struct{}) string {
	path := generatePath(cid, numLeaves)
	_, alreadySeen := seenPaths[path]
	for alreadySeen {
		path = generatePath(cid, numLeaves)
		_, alreadySeen = seenPaths[path]
	}
	return path
}

func generatePath(cid string, numLeaves int) string {
	idx := rand.Intn(numLeaves)
	fmtDirective := binFormattingMap[numLeaves]
	bin := fmt.Sprintf(fmtDirective, idx)
	path := strings.Join(strings.Split(bin, ""), "/")
	return cid + "/" + path
}

type Result struct {
	Elapsed time.Duration
	Err     error
}

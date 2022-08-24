package blockstm

import (
	"fmt"
	"math/big"
	"math/rand"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/log"
)

type OpType int

const readType = 0
const writeType = 1
const otherType = 2

type Op struct {
	key      Key
	duration time.Duration
	opType   OpType
	val      int
}

type testExecTask struct {
	txIdx    int
	ops      []Op
	readMap  map[Key]ReadDescriptor
	writeMap map[Key]WriteDescriptor
	sender   common.Address
	nonce    int
}

type PathGenerator func(common.Address, int) Key

type TaskRunner func(numTx int, numRead int, numWrite int, numNonIO int) (time.Duration, time.Duration)

type Timer func(txIdx int, opIdx int) time.Duration

type Sender func(int) common.Address

func NewTestExecTask(txIdx int, ops []Op, sender common.Address, nonce int) *testExecTask {
	return &testExecTask{
		txIdx:    txIdx,
		ops:      ops,
		readMap:  make(map[Key]ReadDescriptor),
		writeMap: make(map[Key]WriteDescriptor),
		sender:   sender,
		nonce:    nonce,
	}
}

func sleep(i time.Duration) {
	start := time.Now()
	for time.Since(start) < i {
	}
}

func (t *testExecTask) Execute(mvh *MVHashMap, incarnation int) error {
	// Sleep for 50 microsecond to simulate setup time
	sleep(time.Microsecond * 50)

	version := Version{TxnIndex: t.txIdx, Incarnation: incarnation}

	t.readMap = make(map[Key]ReadDescriptor)
	t.writeMap = make(map[Key]WriteDescriptor)

	deps := -1

	for i, op := range t.ops {
		k := op.key

		switch op.opType {
		case readType:
			if _, ok := t.writeMap[k]; ok {
				sleep(op.duration)
				continue
			}

			result := mvh.Read(k, t.txIdx)

			val := result.Value()

			if i == 0 && val != nil && (val.(int) != t.nonce) {
				return ErrExecAbortError{}
			}

			if result.Status() == MVReadResultDependency {
				if result.depIdx > deps {
					deps = result.depIdx
				}
			}

			var readKind int

			if result.Status() == MVReadResultDone {
				readKind = ReadKindMap
			} else if result.Status() == MVReadResultNone {
				readKind = ReadKindStorage
			}

			sleep(op.duration)

			t.readMap[k] = ReadDescriptor{k, readKind, Version{TxnIndex: result.depIdx, Incarnation: result.incarnation}}
		case writeType:
			t.writeMap[k] = WriteDescriptor{k, version, op.val}
		case otherType:
			sleep(op.duration)
		default:
			panic(fmt.Sprintf("Unknown op type: %d", op.opType))
		}
	}

	if deps != -1 {
		return ErrExecAbortError{deps}
	}

	return nil
}

func (t *testExecTask) MVWriteList() []WriteDescriptor {
	return t.MVFullWriteList()
}

func (t *testExecTask) MVFullWriteList() []WriteDescriptor {
	writes := make([]WriteDescriptor, 0, len(t.writeMap))

	for _, v := range t.writeMap {
		writes = append(writes, v)
	}

	return writes
}

func (t *testExecTask) MVReadList() []ReadDescriptor {
	reads := make([]ReadDescriptor, 0, len(t.readMap))

	for _, v := range t.readMap {
		reads = append(reads, v)
	}

	return reads
}

func (t *testExecTask) Sender() common.Address {
	return t.sender
}

func randTimeGenerator(min time.Duration, max time.Duration) func(txIdx int, opIdx int) time.Duration {
	return func(txIdx int, opIdx int) time.Duration {
		return time.Duration(rand.Int63n(int64(max-min))) + min
	}
}

func longTailTimeGenerator(min time.Duration, max time.Duration, i int, j int) func(txIdx int, opIdx int) time.Duration {
	return func(txIdx int, opIdx int) time.Duration {
		if txIdx%i == 0 && opIdx == j {
			return max * 100
		} else {
			return time.Duration(rand.Int63n(int64(max-min))) + min
		}
	}
}

var randomPathGenerator = func(sender common.Address, j int) Key {
	return NewStateKey(sender, common.BigToHash((big.NewInt(int64(j)))))
}

var dexPathGenerator = func(sender common.Address, j int) Key {
	return NewSubpathKey(common.BigToAddress(big.NewInt(int64(0))), 1)
}

var readTime = randTimeGenerator(4*time.Microsecond, 12*time.Microsecond)
var writeTime = randTimeGenerator(2*time.Microsecond, 6*time.Microsecond)
var nonIOTime = randTimeGenerator(1*time.Microsecond, 2*time.Microsecond)

func taskFactory(numTask int, sender Sender, readsPerT int, writesPerT int, nonIOPerT int, pathGenerator PathGenerator, readTime Timer, writeTime Timer, nonIOTime Timer) ([]ExecTask, time.Duration) {
	exec := make([]ExecTask, 0, numTask)

	var serialDuration time.Duration

	senderNonces := make(map[common.Address]int)

	for i := 0; i < numTask; i++ {
		s := sender(i)

		// Set first two ops to always read and write nonce
		ops := make([]Op, 0, readsPerT+writesPerT+nonIOPerT)

		ops = append(ops, Op{opType: readType, key: NewSubpathKey(s, 2), duration: readTime(i, 0), val: senderNonces[s]})

		senderNonces[s]++

		ops = append(ops, Op{opType: writeType, key: NewSubpathKey(s, 2), duration: writeTime(i, 1), val: senderNonces[s]})

		for j := 0; j < readsPerT-1; j++ {
			ops = append(ops, Op{opType: readType})
		}

		for j := 0; j < writesPerT-1; j++ {
			ops = append(ops, Op{opType: writeType})
		}

		for j := 0; j < nonIOPerT; j++ {
			ops = append(ops, Op{opType: otherType})
		}

		// shuffle ops except for the first read op
		for j := 2; j < len(ops); j++ {
			k := rand.Intn(len(ops)-j) + j
			ops[j], ops[k] = ops[k], ops[j]
		}

		for j := 2; j < len(ops); j++ {
			if ops[j].opType == readType {
				ops[j].key = pathGenerator(s, j)
				ops[j].duration = readTime(i, j)
			} else if ops[j].opType == writeType {
				ops[j].key = pathGenerator(s, j)
				ops[j].duration = writeTime(i, j)
			} else {
				ops[j].duration = nonIOTime(i, j)
			}

			serialDuration += ops[j].duration
		}

		t := NewTestExecTask(i, ops, s, senderNonces[s]-1)
		exec = append(exec, t)
	}

	return exec, serialDuration
}

func testExecutorComb(t *testing.T, totalTxs []int, numReads []int, numWrites []int, numNonIO []int, taskRunner TaskRunner) {
	t.Helper()
	log.Root().SetHandler(log.LvlFilterHandler(log.LvlDebug, log.StreamHandler(os.Stderr, log.TerminalFormat(false))))

	improved := 0
	total := 0

	totalExecDuration := time.Duration(0)
	totalSerialDuration := time.Duration(0)

	for _, numTx := range totalTxs {
		for _, numRead := range numReads {
			for _, numWrite := range numWrites {
				for _, numNonIO := range numNonIO {
					log.Info("Executing block", "numTx", numTx, "numRead", numRead, "numWrite", numWrite, "numNonIO", numNonIO)
					execDuration, expectedSerialDuration := taskRunner(numTx, numRead, numWrite, numNonIO)

					if execDuration < expectedSerialDuration {
						improved++
					}
					total++

					performance := "✅"

					if execDuration >= expectedSerialDuration {
						performance = "❌"
					}

					fmt.Printf("exec duration %v, serial duration %v, time reduced %v %.2f%%, %v \n", execDuration, expectedSerialDuration, expectedSerialDuration-execDuration, float64(expectedSerialDuration-execDuration)/float64(expectedSerialDuration)*100, performance)

					totalExecDuration += execDuration
					totalSerialDuration += expectedSerialDuration
				}
			}
		}
	}

	fmt.Println("Improved: ", improved, "Total: ", total, "success rate: ", float64(improved)/float64(total)*100)
	fmt.Printf("Total exec duration: %v, total serial duration: %v, time reduced: %v, time reduced percent: %.2f%%\n", totalExecDuration, totalSerialDuration, totalSerialDuration-totalExecDuration, float64(totalSerialDuration-totalExecDuration)/float64(totalSerialDuration)*100)
}

func runParallel(t *testing.T, tasks []ExecTask, expectedSerialDuration time.Duration, validation func(TxnInputOutput) bool) time.Duration {
	t.Helper()

	start := time.Now()
	txio, _ := ExecuteParallel(tasks)

	// Need to apply the final write set to storage

	finalWriteSet := make(map[Key]time.Duration)

	for _, task := range tasks {
		task := task.(*testExecTask)
		for _, op := range task.ops {
			if op.opType == writeType {
				finalWriteSet[op.key] = op.duration
			}
		}
	}

	for _, v := range finalWriteSet {
		sleep(v)
	}

	duration := time.Since(start)

	if validation != nil {
		assert.True(t, validation(*txio))
	}

	return duration
}

func TestLessConflicts(t *testing.T) {
	t.Parallel()

	totalTxs := []int{10, 50, 100, 200, 300}
	numReads := []int{20, 100, 200}
	numWrites := []int{20, 100, 200}
	numNonIO := []int{100, 500}

	taskRunner := func(numTx int, numRead int, numWrite int, numNonIO int) (time.Duration, time.Duration) {
		sender := func(i int) common.Address {
			randomness := rand.Intn(10) + 10
			return common.BigToAddress(big.NewInt(int64(i % randomness)))
		}
		tasks, serialDuration := taskFactory(numTx, sender, numRead, numWrite, numNonIO, randomPathGenerator, readTime, writeTime, nonIOTime)

		return runParallel(t, tasks, serialDuration, nil), serialDuration
	}

	testExecutorComb(t, totalTxs, numReads, numWrites, numNonIO, taskRunner)
}

func TestAlternatingTx(t *testing.T) {
	t.Parallel()

	totalTxs := []int{200}
	numReads := []int{20}
	numWrites := []int{20}
	numNonIO := []int{100}

	taskRunner := func(numTx int, numRead int, numWrite int, numNonIO int) (time.Duration, time.Duration) {
		sender := func(i int) common.Address { return common.BigToAddress(big.NewInt(int64(i % 2))) }
		tasks, serialDuration := taskFactory(numTx, sender, numRead, numWrite, numNonIO, randomPathGenerator, readTime, writeTime, nonIOTime)

		return runParallel(t, tasks, serialDuration, nil), serialDuration
	}

	testExecutorComb(t, totalTxs, numReads, numWrites, numNonIO, taskRunner)
}

func TestMoreConflicts(t *testing.T) {
	t.Parallel()

	totalTxs := []int{10, 50, 100, 200, 300}
	numReads := []int{20, 100, 200}
	numWrites := []int{20, 100, 200}
	numNonIO := []int{100, 500}

	taskRunner := func(numTx int, numRead int, numWrite int, numNonIO int) (time.Duration, time.Duration) {
		sender := func(i int) common.Address {
			randomness := rand.Intn(10) + 10
			return common.BigToAddress(big.NewInt(int64(i / randomness)))
		}
		tasks, serialDuration := taskFactory(numTx, sender, numRead, numWrite, numNonIO, randomPathGenerator, readTime, writeTime, nonIOTime)

		return runParallel(t, tasks, serialDuration, nil), serialDuration
	}

	testExecutorComb(t, totalTxs, numReads, numWrites, numNonIO, taskRunner)
}

func TestRandomTx(t *testing.T) {
	t.Parallel()

	totalTxs := []int{10, 50, 100, 200, 300}
	numReads := []int{20, 100, 200}
	numWrites := []int{20, 100, 200}
	numNonIO := []int{100, 500}

	taskRunner := func(numTx int, numRead int, numWrite int, numNonIO int) (time.Duration, time.Duration) {
		// Randomly assign this tx to one of 10 senders
		sender := func(i int) common.Address { return common.BigToAddress(big.NewInt(int64(rand.Intn(10)))) }
		tasks, serialDuration := taskFactory(numTx, sender, numRead, numWrite, numNonIO, randomPathGenerator, readTime, writeTime, nonIOTime)

		return runParallel(t, tasks, serialDuration, nil), serialDuration
	}

	testExecutorComb(t, totalTxs, numReads, numWrites, numNonIO, taskRunner)
}

func TestTxWithLongTailRead(t *testing.T) {
	t.Parallel()

	totalTxs := []int{10, 50, 100, 200, 300}
	numReads := []int{20, 100, 200}
	numWrites := []int{20, 100, 200}
	numNonIO := []int{100, 500}

	taskRunner := func(numTx int, numRead int, numWrite int, numNonIO int) (time.Duration, time.Duration) {
		sender := func(i int) common.Address {
			randomness := rand.Intn(10) + 10
			return common.BigToAddress(big.NewInt(int64(i / randomness)))
		}

		longTailReadTimer := longTailTimeGenerator(4*time.Microsecond, 12*time.Microsecond, 7, 10)

		tasks, serialDuration := taskFactory(numTx, sender, numRead, numWrite, numNonIO, randomPathGenerator, longTailReadTimer, writeTime, nonIOTime)

		return runParallel(t, tasks, serialDuration, nil), serialDuration
	}

	testExecutorComb(t, totalTxs, numReads, numWrites, numNonIO, taskRunner)
}

func TestDexScenario(t *testing.T) {
	t.Parallel()

	totalTxs := []int{10, 50, 100, 200, 300}
	numReads := []int{20, 100, 200}
	numWrites := []int{20, 100, 200}
	numNonIO := []int{100, 500}

	validation := func(txio TxnInputOutput) bool {
		for i, inputs := range txio.inputs {
			for _, input := range inputs {
				if input.Path.IsSubpath() && input.Path.GetSubpath() != 2 && input.V.TxnIndex != i-1 {
					return false
				}
			}
		}

		return true
	}

	taskRunner := func(numTx int, numRead int, numWrite int, numNonIO int) (time.Duration, time.Duration) {
		sender := func(i int) common.Address { return common.BigToAddress(big.NewInt(int64(i))) }
		tasks, serialDuration := taskFactory(numTx, sender, numRead, numWrite, numNonIO, dexPathGenerator, readTime, writeTime, nonIOTime)

		return runParallel(t, tasks, serialDuration, validation), serialDuration
	}

	testExecutorComb(t, totalTxs, numReads, numWrites, numNonIO, taskRunner)
}

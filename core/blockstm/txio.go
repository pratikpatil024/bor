//nolint: unused
package blockstm

const (
	ReadKindMap     = 0
	ReadKindStorage = 1
)

type ReadDescriptor struct {
	Path Key
	Kind int
	V    Version
}

type WriteDescriptor struct {
	Path Key
	V    Version
	Val  interface{}
}

type TxnInput []ReadDescriptor
type TxnOutput []WriteDescriptor

// hasNewWrite: returns true if the current set has a new write compared to the input
func (txo TxnOutput) hasNewWrite(cmpSet []WriteDescriptor) bool {
	if len(txo) == 0 {
		return false
	} else if len(cmpSet) == 0 || len(txo) > len(cmpSet) {
		return true
	}

	cmpMap := map[Key]bool{cmpSet[0].Path: true}

	for i := 1; i < len(cmpSet); i++ {
		cmpMap[cmpSet[i].Path] = true
	}

	for _, v := range txo {
		if !cmpMap[v.Path] {
			return true
		}
	}

	return false
}

type TxnInputOutput struct {
	inputs     []TxnInput
	outputs    []TxnOutput // write sets that should be checked during validation
	allOutputs []TxnOutput // entire write sets in MVHashMap. allOutputs should always be a parent set of outputs
}

func (io *TxnInputOutput) ReadSet(txnIdx int) []ReadDescriptor {
	return io.inputs[txnIdx]
}

func (io *TxnInputOutput) WriteSet(txnIdx int) []WriteDescriptor {
	return io.outputs[txnIdx]
}

func (io *TxnInputOutput) AllWriteSet(txnIdx int) []WriteDescriptor {
	return io.allOutputs[txnIdx]
}

func MakeTxnInputOutput(numTx int) *TxnInputOutput {
	return &TxnInputOutput{
		inputs:     make([]TxnInput, numTx),
		outputs:    make([]TxnOutput, numTx),
		allOutputs: make([]TxnOutput, numTx),
	}
}

func (io *TxnInputOutput) recordRead(txId int, input []ReadDescriptor) {
	io.inputs[txId] = input
}

func (io *TxnInputOutput) recordWrite(txId int, output []WriteDescriptor) {
	io.outputs[txId] = output
}

func (io *TxnInputOutput) recordAllWrite(txId int, output []WriteDescriptor) {
	io.allOutputs[txId] = output
}

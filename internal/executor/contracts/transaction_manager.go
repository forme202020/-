package contracts

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/meshplus/bitxhub-model/constant"
	"github.com/sirupsen/logrus"

	"github.com/looplab/fsm"

	"github.com/meshplus/bitxhub-core/boltvm"
	"github.com/meshplus/bitxhub-model/pb"
)

const (
	PREFIX        = "tx"
	TimeoutPrefix = "timeout"
)

type TransactionManager struct {
	boltvm.Stub
	fsm *fsm.FSM
}

type TransactionInfo struct {
	GlobalState  pb.TransactionStatus
	Height       uint64
	ChildTxInfo  map[string]pb.TransactionStatus
	ChildTxCount uint64
}

type TransactionEvent string

func (e TransactionEvent) String() string {
	return string(e)
}

const (
	TransactionEventBegin        TransactionEvent = "begin"
	TransactionEventBeginFailure TransactionEvent = "begin_failure"
	TransactionEventTimeout      TransactionEvent = "timeout"
	TransactionEventFailure      TransactionEvent = "failure"
	TransactionEventSuccess      TransactionEvent = "success"
	TransactionEventRollback     TransactionEvent = "rollback"
	TransactionEventDstRollback  TransactionEvent = "dst_rollback"
	TransactionEventDstFailure   TransactionEvent = "dst_failure"
	TransactionStateInit                          = "init"
)

var receipt2EventM = map[int32]TransactionEvent{
	int32(pb.IBTP_RECEIPT_FAILURE):  TransactionEventFailure,
	int32(pb.IBTP_RECEIPT_SUCCESS):  TransactionEventSuccess,
	int32(pb.IBTP_RECEIPT_ROLLBACK): TransactionEventRollback,
}

var txStatus2EventM = map[int32]TransactionEvent{
	int32(pb.TransactionStatus_BEGIN_FAILURE):  TransactionEventDstFailure,
	int32(pb.TransactionStatus_BEGIN_ROLLBACK): TransactionEventDstRollback,
}

func (t *TransactionManager) BeginMultiTXs(globalID, ibtpID string, timeoutHeight uint64, isFailed bool, count uint64) *boltvm.Response {
	if bxhErr := t.checkCurrentCaller(); bxhErr != nil {
		return boltvm.Error(bxhErr.Code, string(bxhErr.Msg))
	}

	change := pb.StatusChange{PrevStatus: -1}
	txInfo := TransactionInfo{}
	if ok := t.GetObject(GlobalTxInfoKey(globalID), &txInfo); !ok {
		txInfo = TransactionInfo{
			GlobalState:  pb.TransactionStatus_BEGIN,
			ChildTxInfo:  map[string]pb.TransactionStatus{ibtpID: pb.TransactionStatus_BEGIN},
			ChildTxCount: count,
		}

		if timeoutHeight == 0 || timeoutHeight >= math.MaxUint64-t.GetCurrentHeight() {
			txInfo.Height = math.MaxUint64
		} else {
			txInfo.Height = t.GetCurrentHeight() + timeoutHeight
		}
		if isFailed {
			txInfo.ChildTxInfo[ibtpID] = pb.TransactionStatus_BEGIN_FAILURE
			txInfo.GlobalState = pb.TransactionStatus_BEGIN_FAILURE
		} else {
			t.addToTimeoutList(txInfo.Height, globalID)
		}

		t.AddObject(GlobalTxInfoKey(globalID), txInfo)
	} else {
		if _, ok := txInfo.ChildTxInfo[ibtpID]; ok {
			return boltvm.Error(boltvm.TransactionExistentChildTxCode, fmt.Sprintf(string(boltvm.TransactionExistentChildTxMsg), ibtpID, globalID))
		}
		// globalState is fail, child state is fail
		if txInfo.GlobalState != pb.TransactionStatus_BEGIN {
			txInfo.ChildTxInfo[ibtpID] = txInfo.GlobalState
		} else {
			if isFailed {
				for key, childStatus := range txInfo.ChildTxInfo {
					// need all child ibtps which received success receipt rollback on src chain,
					// but just need success status ibtp rollback on dest chain,
					// because if child ibtp's status is begin, bxh can handle dest rollback when it receives the receipt
					if childStatus == pb.TransactionStatus_SUCCESS {
						change.NotifyDstIBTPIDs = append(change.NotifyDstIBTPIDs, key)
					}
					change.NotifySrcIBTPIDs = append(change.NotifySrcIBTPIDs, key)
					txInfo.ChildTxInfo[key] = pb.TransactionStatus_BEGIN_FAILURE
				}
				txInfo.ChildTxInfo[ibtpID] = pb.TransactionStatus_BEGIN_FAILURE
				txInfo.GlobalState = pb.TransactionStatus_BEGIN_FAILURE
				t.removeFromTimeoutList(txInfo.Height, globalID)
			} else {
				txInfo.ChildTxInfo[ibtpID] = pb.TransactionStatus_BEGIN
			}
		}
		t.SetObject(GlobalTxInfoKey(globalID), txInfo)
	}
	// record globalID
	t.Set(ibtpID, []byte(globalID))

	change.CurStatus = txInfo.ChildTxInfo[ibtpID]
	for id := range txInfo.ChildTxInfo {
		change.ChildIBTPIDs = append(change.ChildIBTPIDs, id)
	}

	data, err := change.Marshal()
	if err != nil {
		return boltvm.Error(boltvm.TransactionInternalErrCode, err.Error())
	}
	t.Logger().WithFields(logrus.Fields{"id": ibtpID, "globalID": globalID, "change": change, "globalState": txInfo}).Info("BeginMultiTXs")
	return boltvm.Success(data)
}

func (t *TransactionManager) Begin(txId string, timeoutHeight uint64, isFailed bool) *boltvm.Response {
	if bxhErr := t.checkCurrentCaller(); bxhErr != nil {
		return boltvm.Error(bxhErr.Code, string(bxhErr.Msg))
	}

	record := pb.TransactionRecord{
		Status: pb.TransactionStatus_BEGIN,
		Height: t.GetCurrentHeight() + timeoutHeight,
	}

	if timeoutHeight == 0 || timeoutHeight >= math.MaxUint64-t.GetCurrentHeight() {
		record.Height = math.MaxUint64
	}

	if isFailed {
		record.Status = pb.TransactionStatus_BEGIN_FAILURE
	} else {
		// t.addToTimeoutList(record.Height, txId)
	}

	recordData, err := record.Marshal()
	if err != nil {
		return boltvm.Error(boltvm.TransactionInternalErrCode, err.Error())
	}
	t.Add(TxInfoKey(txId), recordData)

	change := pb.StatusChange{
		PrevStatus: -1,
		CurStatus:  record.Status,
	}

	data, err := change.Marshal()
	if err != nil {
		return boltvm.Error(boltvm.TransactionInternalErrCode, err.Error())
	}

	return boltvm.Success(data)
}

// BeginInterBitXHub transaction management for inter-bitxhub transaction
// - if curStatus == BEGIN && txStatus == BEGIN_FAIL, then change curStatus to FAIL and notify src chain
// - if curStatus == BEGIN && txStatus == BEGIN_ROLLBACK, then change curStatus to ROLLBACK and notify src chain
// - isFailed note that whether dstBitXHub is available
func (t *TransactionManager) BeginInterBitXHub(txId string, timeoutHeight uint64, proof []byte, isFailed bool) *boltvm.Response {
	if bxhErr := t.checkCurrentCaller(); bxhErr != nil {
		return boltvm.Error(bxhErr.Code, string(bxhErr.Msg))
	}

	change := pb.StatusChange{}
	var record pb.TransactionRecord
	ok, _ := t.Get(TxInfoKey(txId))
	if ok {
		bxhProof := &pb.BxhProof{}
		if err := bxhProof.Unmarshal(proof); err != nil {
			return boltvm.Error(boltvm.TransactionStateErrCode, fmt.Sprintf("unmarshal proof from dst BitXHub for ibtp %s failed: %s", txId, err.Error()))
		}
		txStatus := int32(bxhProof.TxStatus)
		change.PrevStatus = record.Status
		if err := t.setFSM(&record.Status, txStatus2EventM[txStatus]); err != nil {
			return boltvm.Error(boltvm.TransactionStateErrCode, fmt.Sprintf(string(boltvm.TransactionStateErrMsg), fmt.Sprintf("transaction %s with state %v get unexpected receipt %v", txId, record.Status, txStatus)))
		}
		change.CurStatus = record.Status

		recordData, err := record.Marshal()
		if err != nil {
			return boltvm.Error(boltvm.TransactionInternalErrCode, err.Error())
		}
		t.Add(TxInfoKey(txId), recordData)
	} else {
		record = pb.TransactionRecord{
			Status: pb.TransactionStatus_BEGIN,
			Height: t.GetCurrentHeight() + timeoutHeight,
		}

		if timeoutHeight == 0 || timeoutHeight >= math.MaxUint64-t.GetCurrentHeight() {
			record.Height = math.MaxUint64
		}

		if isFailed {
			record.Status = pb.TransactionStatus_BEGIN_FAILURE
		}

		recordData, err := record.Marshal()
		if err != nil {
			return boltvm.Error(boltvm.TransactionInternalErrCode, err.Error())
		}
		t.Add(TxInfoKey(txId), recordData)

		change = pb.StatusChange{
			PrevStatus: -1,
			CurStatus:  record.Status,
		}
	}

	data, err := change.Marshal()
	if err != nil {
		return boltvm.Error(boltvm.TransactionInternalErrCode, err.Error())
	}

	return boltvm.Success(data)
}

func (t *TransactionManager) Report(txId string, result int32) *boltvm.Response {
	if bxhErr := t.checkCurrentCaller(); bxhErr != nil {
		return boltvm.Error(bxhErr.Code, string(bxhErr.Msg))
	}

	var (
		change   pb.StatusChange
		globalId string
	)
	if ok, recordData := t.Get(TxInfoKey(txId)); ok {
		record := pb.TransactionRecord{}
		err := record.Unmarshal(recordData)
		if err != nil {
			return boltvm.Error(boltvm.TransactionInternalErrCode, err.Error())
		}

		change.PrevStatus = record.Status
		if err := t.setFSM(&record.Status, receipt2EventM[result]); err != nil {
			return boltvm.Error(boltvm.TransactionStateErrCode, fmt.Sprintf(string(boltvm.TransactionStateErrMsg), fmt.Sprintf("transaction %s with state %v get unexpected receipt %v", txId, record.Status, result)))
		}
		change.CurStatus = record.Status

		data, err := record.Marshal()
		if err != nil {
			return boltvm.Error(boltvm.TransactionInternalErrCode, err.Error())
		}
		t.Set(TxInfoKey(txId), data)
	} else {
		ok, val := t.Get(txId)
		if !ok {
			return boltvm.Error(boltvm.TransactionNonexistentTxCode, fmt.Sprintf(string(boltvm.TransactionNonexistentTxMsg), txId))
		}

		globalId = string(val)
		txInfo := TransactionInfo{}
		if !t.GetObject(GlobalTxInfoKey(globalId), &txInfo) {
			return boltvm.Error(boltvm.TransactionNonexistentGlobalTxCode, fmt.Sprintf(string(boltvm.TransactionNonexistentGlobalTxMsg), globalId, txId))
		}

		_, ok = txInfo.ChildTxInfo[txId]
		if !ok {
			return boltvm.Error(boltvm.TransactionInternalErrCode, fmt.Sprintf("%s is not in transaction %s, %v", txId, globalId, txInfo))
		}

		change.PrevStatus = txInfo.GlobalState
		if err := t.changeMultiTxStatus(globalId, &txInfo, txId, result); err != nil {
			return boltvm.Error(boltvm.TransactionStateErrCode, err.Error())
		}
		change.CurStatus = txInfo.GlobalState

		for key, childStatus := range txInfo.ChildTxInfo {
			if key != txId {
				// when the child ibtp's receipt is fail,
				// wrapper child ibtp which status is success to notify dest chain rollback
				if change.PrevStatus == pb.TransactionStatus_BEGIN && change.CurStatus == pb.TransactionStatus_BEGIN_FAILURE {
					// current child IBTP needn't notify dest chain rollback,
					// because it has already update InCounter and not executed successfully
					change.IsFailChildIBTP = true
					if childStatus == pb.TransactionStatus_SUCCESS {
						change.NotifyDstIBTPIDs = append(change.NotifyDstIBTPIDs, key)
					}
				}
				// when bxh receive all success receipt, globalStatus modify from begin->success
				// wrapper all child ibtpid in multiIBTPs to notify src chain
				change.NotifySrcIBTPIDs = append(change.NotifySrcIBTPIDs, key)
			}
		}
		change.ChildIBTPIDs = make([]string, 0, len(txInfo.ChildTxInfo))
		for id := range txInfo.ChildTxInfo {
			change.ChildIBTPIDs = append(change.ChildIBTPIDs, id)
		}
		// ensure all nodes ChildIBTPIDs is equal
		sort.Strings(change.ChildIBTPIDs)

		t.SetObject(GlobalTxInfoKey(globalId), txInfo)
		t.Logger().WithFields(logrus.Fields{"id": txId, "globalID": globalId, "change": change, "globalState": txInfo}).Info("Report")
	}

	data, err := change.Marshal()
	if err != nil {
		return boltvm.Error(boltvm.TransactionInternalErrCode, err.Error())
	}

	return boltvm.Success(data)
}

func (t *TransactionManager) GetStatus(txId string) *boltvm.Response {
	ok, recordData := t.Get(TxInfoKey(txId))
	if ok {
		record := pb.TransactionRecord{}
		err := record.Unmarshal(recordData)
		if err != nil {
			return boltvm.Error(boltvm.TransactionInternalErrCode, err.Error())
		}
		status := record.Status
		return boltvm.Success([]byte(strconv.Itoa(int(status))))
	}

	txInfo := TransactionInfo{}
	ok = t.GetObject(GlobalTxInfoKey(txId), &txInfo)
	if ok {
		return boltvm.Success([]byte(strconv.Itoa(int(txInfo.GlobalState))))
	}

	ok, val := t.Get(txId)
	if !ok {
		return boltvm.Error(boltvm.TransactionNonexistentGlobalIdCode, fmt.Sprintf(string(boltvm.TransactionNonexistentGlobalIdMsg), txId))
	}

	globalId := string(val)
	txInfo = TransactionInfo{}
	if !t.GetObject(GlobalTxInfoKey(globalId), &txInfo) {
		return boltvm.Error(boltvm.TransactionNonexistentGlobalTxCode, fmt.Sprintf(string(boltvm.TransactionNonexistentGlobalTxMsg), globalId, txId))
	}

	return boltvm.Success([]byte(strconv.Itoa(int(txInfo.GlobalState))))
}

func (t *TransactionManager) setFSM(state *pb.TransactionStatus, event TransactionEvent) error {
	callbackFunc := func(event *fsm.Event) {
		*state = pb.TransactionStatus(pb.TransactionStatus_value[event.FSM.Current()])
	}

	t.fsm = fsm.NewFSM(
		state.String(),
		fsm.Events{
			{Name: TransactionEventBegin.String(), Src: []string{TransactionStateInit}, Dst: pb.TransactionStatus_BEGIN.String()},
			{Name: TransactionEventBeginFailure.String(), Src: []string{TransactionStateInit, pb.TransactionStatus_BEGIN.String()}, Dst: pb.TransactionStatus_BEGIN_FAILURE.String()},
			{Name: TransactionEventTimeout.String(), Src: []string{pb.TransactionStatus_BEGIN.String()}, Dst: pb.TransactionStatus_BEGIN_ROLLBACK.String()},
			{Name: TransactionEventSuccess.String(), Src: []string{pb.TransactionStatus_BEGIN.String()}, Dst: pb.TransactionStatus_SUCCESS.String()},
			{Name: TransactionEventFailure.String(), Src: []string{pb.TransactionStatus_BEGIN.String(), pb.TransactionStatus_BEGIN_FAILURE.String()}, Dst: pb.TransactionStatus_FAILURE.String()},
			{Name: TransactionEventRollback.String(), Src: []string{pb.TransactionStatus_BEGIN_ROLLBACK.String()}, Dst: pb.TransactionStatus_ROLLBACK.String()},
			// if receive receipt fail, the previous status is begin rollback
			{Name: TransactionEventFailure.String(), Src: []string{pb.TransactionStatus_BEGIN_ROLLBACK.String()}, Dst: pb.TransactionStatus_ROLLBACK.String()},
			{Name: TransactionEventDstFailure.String(), Src: []string{pb.TransactionStatus_BEGIN.String()}, Dst: pb.TransactionStatus_FAILURE.String()},
			{Name: TransactionEventDstRollback.String(), Src: []string{pb.TransactionStatus_BEGIN.String()}, Dst: pb.TransactionStatus_ROLLBACK.String()},
		},
		fsm.Callbacks{
			TransactionEventBegin.String():        callbackFunc,
			TransactionEventBeginFailure.String(): callbackFunc,
			TransactionEventTimeout.String():      callbackFunc,
			TransactionEventSuccess.String():      callbackFunc,
			TransactionEventFailure.String():      callbackFunc,
			TransactionEventRollback.String():     callbackFunc,
			TransactionEventDstFailure.String():   callbackFunc,
			TransactionEventDstRollback.String():  callbackFunc,
		},
	)

	return t.fsm.Event(event.String())
}

func (t *TransactionManager) addToTimeoutList(height uint64, txId string) {
	var timeoutList string
	var builder strings.Builder
	ok, val := t.Get(TimeoutKey(height))
	if !ok {
		timeoutList = txId
	} else {
		timeoutList = string(val)
		builder.WriteString(timeoutList)
		builder.WriteString(",")
		builder.WriteString(txId)
		timeoutList = builder.String()
	}
	t.Set(TimeoutKey(height), []byte(timeoutList))
}

func (t *TransactionManager) removeFromTimeoutList(height uint64, txId string) {
	ok, timeoutList := t.Get(TimeoutKey(height))
	if ok {
		list := strings.Split(string(timeoutList), ",")
		for index, value := range list {
			if value == txId {
				list = append(list[:index], list[index+1:]...)
			}
		}
		t.Set(TimeoutKey(height), []byte(strings.Join(list, ",")))
	}
}

func (t *TransactionManager) checkCurrentCaller() *boltvm.BxhError {
	if t.CurrentCaller() != constant.InterchainContractAddr.Address().String() {
		return boltvm.BError(boltvm.TransactionNoPermissionCode, fmt.Sprintf(string(boltvm.TransactionNoPermissionMsg), t.CurrentCaller()))
	}

	return nil
}

func (t *TransactionManager) changeMultiTxStatus(globalID string, txInfo *TransactionInfo, txId string, result int32) error {
	if txInfo.GlobalState == pb.TransactionStatus_BEGIN && result == int32(pb.IBTP_RECEIPT_FAILURE) {
		for childTx := range txInfo.ChildTxInfo {
			txInfo.ChildTxInfo[childTx] = pb.TransactionStatus_BEGIN_FAILURE
		}
		txInfo.ChildTxInfo[txId] = pb.TransactionStatus_FAILURE
		txInfo.GlobalState = pb.TransactionStatus_BEGIN_FAILURE

		t.removeFromTimeoutList(txInfo.Height, globalID)

		return nil
	} else {
		status := txInfo.ChildTxInfo[txId]

		// if bxh had received child IBTP receipt success/fail/rollback, but other child IBTP receipt missing,
		// pier need handleMissing all child IBTP because of child IBTP's SrcReceiptCounter does not add 1
		// so bxh will return err with the ibtp had already reached the final state to pier.
		if err := t.setFSM(&status, receipt2EventM[result]); err != nil {
			return fmt.Errorf("child tx %s with state %v get unexpected receipt %v", txId, status, result)
		}

		txInfo.ChildTxInfo[txId] = status

		if isMultiTxFinished(status, txInfo) {
			if err := t.setFSM(&txInfo.GlobalState, receipt2EventM[result]); err != nil {
				return fmt.Errorf("global tx of child tx %s with state %v get unexpected receipt %v", txId, status, result)
			}

			t.removeFromTimeoutList(txInfo.Height, globalID)

			return nil
		}
	}

	return nil
}

func isMultiTxFinished(childStatus pb.TransactionStatus, txInfo *TransactionInfo) bool {
	count := uint64(0)
	for _, res := range txInfo.ChildTxInfo {
		if res != childStatus {
			return false
		}
		count++
	}

	return count == txInfo.ChildTxCount
}

func TxInfoKey(id string) string {
	return fmt.Sprintf("%s-%s", PREFIX, id)
}

func GlobalTxInfoKey(id string) string {
	return fmt.Sprintf("global-%s-%s", PREFIX, id)
}

func TimeoutKey(height uint64) string {
	return fmt.Sprintf("%s-%d", TimeoutPrefix, height)
}

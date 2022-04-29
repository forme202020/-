package grpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/meshplus/bitxhub-core/tss"
	"github.com/meshplus/bitxhub-core/tss/conversion"
	"github.com/meshplus/bitxhub-model/pb"
	"github.com/meshplus/bitxhub/internal/model"
	"github.com/meshplus/bitxhub/pkg/utils"
	"github.com/sirupsen/logrus"
)

func (cbs *ChainBrokerService) GetMultiSigns(ctx context.Context, req *pb.GetSignsRequest) (*pb.SignResponse, error) {
	var (
		wg     = sync.WaitGroup{}
		result = make(map[string][]byte)
	)

	signers := []string{}
	for id := range cbs.api.Network().OtherPeers() {
		signers = append(signers, strconv.Itoa(int(id)))
	}
	req.Extra = []byte(strings.Join(signers, ","))

	wg.Add(1)
	go func(result map[string][]byte) {
		for k, v := range cbs.api.Broker().FetchSignsFromOtherPeers(req) {
			result[k] = v
		}
		wg.Done()
	}(result)

	address, sign, _, err := cbs.api.Broker().GetSign(req, nil)
	wg.Wait()

	if err != nil {
		cbs.logger.WithFields(logrus.Fields{
			"id":  req.Content,
			"err": err.Error(),
		}).Errorf("Get sign on current node")
		return nil, fmt.Errorf("get sign on current node failed: %w", err)
	} else {
		result[address] = sign
	}
	return &pb.SignResponse{
		Sign: result,
	}, nil
}

func (cbs *ChainBrokerService) GetTssSigns(ctx context.Context, req *pb.GetSignsRequest) (*pb.SignResponse, error) {
	var (
		wg     = sync.WaitGroup{}
		result = [][]byte{}
	)

	// 1. check req type
	if !utils.IsTssReq(req) {
		return nil, fmt.Errorf("req type is not tss req")
	}

	// 2. get tss info
	signersALL := []string{}
	poolPkData := []byte{}
	// todo fbz:从内存拿tss
	tssInfo, err := cbs.api.Broker().GetTssInfo()
	tssFlag := true
	if err != nil {
		// 当前节点没有tss信息，向其他节点请求
		tssInfos := cbs.api.Broker().FetchTssInfoFromOtherPeers()
		signersALL, poolPkData, err = getConsensusTssInfoParties(tssInfos, cbs.api.Broker().GetQuorum())
		if err != nil {
			return nil, fmt.Errorf("get tss info from other peers error: %v", err)
		}
		tssFlag = false
	} else {
		for id, _ := range tssInfo.PartiesPkMap {
			signersALL = append(signersALL, id)
		}
		poolPkData = tssInfo.Pubkey
	}
	poolPk, err := conversion.GetECDSAPubKeyFromPubKeyData(poolPkData)
	if err != nil {
		return nil, fmt.Errorf("failed to get ECDSA pubKey from pubkey data: %v", err)
	}

	// 3. make a tss req with threshold signers
	tssReq := &pb.GetSignsRequest{
		Type:    req.Type,
		Content: req.Content,
	}

	for {
		// 4. check signers num
		if len(signersALL) < int(cbs.api.Broker().GetQuorum()) {
			cbs.logger.WithFields(logrus.Fields{
				"signers": signersALL,
			}).Errorf("too less signers")
			break
		}

		// 5. choose signers randomly
		nums := RandRangeNumbers(0, len(signersALL)-1, int(cbs.api.Broker().GetQuorum()))
		tssSigners := []string{}
		for _, i := range nums {
			tssSigners = append(tssSigners, signersALL[i])
		}
		cbs.logger.Infof("====================== tss all signers: %s, signers: %s", strings.Join(signersALL, ","), strings.Join(tssSigners, ","))
		tssReq.Extra = []byte(strings.Join(tssSigners, ","))

		// 6. send sign req to others
		wg.Add(1)
		go func() {
			cbs.api.Broker().FetchSignsFromOtherPeers(tssReq)
			tssSignResCh := make(chan *pb.Message)
			tssSignResSub := cbs.api.Feed().SubscribeTssSignRes(tssSignResCh)
			defer tssSignResSub.Unsubscribe()
			for {
				select {
				case m := <-tssSignResCh:
					signRes := &model.MerkleWrapperSign{}
					if err := signRes.Unmarshal(m.Data); err != nil {
						cbs.logger.WithFields(logrus.Fields{
							"err": err,
						}).Errorf("unmarshal sign res error")
						continue
						//return
					}

					if err := utils.VerifyTssSigns(signRes.Signature, poolPk, cbs.logger); err != nil {
						cbs.logger.WithFields(logrus.Fields{}).Errorf("Verify tss signs error")
						//break
					} else {
						result = append(result, signRes.Signature)
						cbs.logger.WithFields(logrus.Fields{}).Debug("get verified tss signature from other peers")
						wg.Done()
						return
					}
				case <-time.After(cbs.config.Tss.KeySignTimeout):
					cbs.logger.WithFields(logrus.Fields{}).Warnf("wait for sign from other peers timeout: %v", cbs.config.Tss.KeySignTimeout)
					wg.Done()
					return
				}
			}
		}()

		// 7. get sign by ourself
		if tssFlag {
			addr, sign, culprits, err := cbs.api.Broker().GetSign(tssReq, tssSigners)
			wg.Wait()
			// todo fbz: 优先取result结果
			if err == nil {
				return &pb.SignResponse{
					Sign: map[string][]byte{
						addr: convertSignData(sign),
					},
				}, nil
			} else if errors.Is(err, tss.ErrNotActiveSigner) {
				if len(result) != 0 {
					return &pb.SignResponse{
						Sign: map[string][]byte{
							addr: convertSignData(result[0]),
						},
					}, nil
				} else {
					return nil, fmt.Errorf("get tss signs error")
				}
			}
			// 8. handle culprits
			cbs.logger.WithFields(logrus.Fields{
				"id":       req.Content,
				"culprits": culprits,
				"err":      err.Error(),
			}).Errorf("Get tss sign on current node")

			if culprits == nil {
				return nil, err
			}
			for _, idC := range culprits {
				for i, idS := range signersALL {
					if idC == idS {
						if i == 0 {
							signersALL = signersALL[1:]
						} else {
							signersALL = append(signersALL[:i-1], signersALL[i+1:]...)
						}
					}
				}
			}
		} else {
			// 自己不是签名节点，无法签名
			wg.Wait()
			if len(result) != 0 {
				return &pb.SignResponse{
					Sign: map[string][]byte{
						cbs.api.Broker().GetPrivKey().Address: convertSignData(result[0]),
					},
				}, nil
			} else {
				return nil, fmt.Errorf("get tss signs error")
			}
		}
	}

	return nil, fmt.Errorf("GetTSSSigns error")

}

func convertSignData(signData []byte) []byte {
	signs := []conversion.Signature{}
	_ = json.Unmarshal(signData, &signs)

	signs1 := [][]byte{}
	for _, sign := range signs {
		signs1 = append(signs1, sign.SignEthData)
	}

	signs1Data, _ := json.Marshal(signs1)

	return signs1Data
}

func RandRangeNumbers(min, max, count int) []int {
	//检查参数
	if max < min || (max-min+1) < count {
		return nil
	}
	nums := make([]int, max-min+1)
	position := -1            //记录nums[-min]的位置
	if min <= 0 && max >= 0 { //获取范围内有0，则先用max+1代替
		position = -min
		nums[position] = max + 1
	}
	//随机数生成器，加入时间戳保证每次生成的随机数不一样
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	for i := 0; i < count; i++ {
		num := r.Intn(max - min + 1)
		if nums[i] == 0 { //代表未赋值
			nums[i] = min + i
		}
		if nums[num] == 0 { //代表未赋值
			nums[num] = min + num
		}
		if position != -1 { //此时需要记录位置
			if i == position {
				position = num
			} else if num == position {
				position = i
			}
		}
		nums[i], nums[num] = nums[num], nums[i]
	}

	if position != -1 { //证明有位置记录，则还原为0
		nums[position] = 0
	}
	return nums[0:count]
}

func getConsensusTssInfoParties(infos []*pb.TssInfo, quorum uint64) ([]string, []byte, error) {
	freqInfos := make(map[string]int, len(infos))
	for _, info := range infos {
		ids := []string{}
		for id, _ := range info.PartiesPkMap {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool {
			return ids[i] > ids[j]
		})
		idsStr := strings.Join(ids, ",")
		infosStr := fmt.Sprintf("%s-%s", idsStr, string(info.Pubkey))
		freqInfos[infosStr]++
	}
	maxFreq := -1
	var consensusInfo string
	for infoStr, counter := range freqInfos {
		if counter > maxFreq {
			maxFreq = counter
			consensusInfo = infoStr
		}
	}

	if maxFreq < int(quorum) {
		return nil, nil, fmt.Errorf("there is no consensus parties, maxFreq: %d, quorum: %d", maxFreq, quorum)
	}

	idsAddr := strings.Split(strings.Split(consensusInfo, "-")[0], ",")
	pk := strings.Split(consensusInfo, "-")[1]

	return idsAddr, []byte(pk), nil
}

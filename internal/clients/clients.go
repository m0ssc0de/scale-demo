package clients

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go-dictionary/internal/config"
	"go-dictionary/internal/db/rocksdb"
	"go-dictionary/internal/messages"
	"io/ioutil"
	"strings"
	"time"

	trieNode "go-dictionary/internal/trie/node"

	"github.com/hashicorp/go-retryablehttp"
	scalecodec "github.com/itering/scale.go"
	"github.com/itering/scale.go/source"
	"github.com/itering/scale.go/types"
	"github.com/itering/scale.go/utiles"
	"github.com/itering/substrate-api-rpc/rpc"
)

type (
	BareClient struct {
		configuration config.Config
		rdbClient     *rocksdb.RockClient
	}
)

// getStateRootFromRawHeader gets the state root from a decoded block header
func getStateRootFromRawHeader(rawHeader interface{}) string {
	stateRoot, ok := rawHeader.(map[string]interface{})["state_root"].(string)
	if !ok {
		messages.NewDictionaryMessage(
			messages.LOG_LEVEL_ERROR,
			messages.GetComponent(getStateRootFromRawHeader),
			nil,
			"Can not found state root",
		).ConsoleLog()
	}

	return strings.TrimPrefix(stateRoot, "0x")
}

func NewBareClient(config config.Config) *BareClient {
	messages.NewDictionaryMessage(
		messages.LOG_LEVEL_INFO,
		"",
		nil,
		"Create Bare Client",
	).ConsoleLog()
	rdbClient := rocksdb.OpenRocksdb(config.RocksdbConfig)
	lastBlock := rdbClient.GetLastBlockSynced()
	messages.NewDictionaryMessage(
		messages.LOG_LEVEL_INFO,
		"",
		nil,
		"Last BLock in RocksDB %v", lastBlock,
	).ConsoleLog()
	return &BareClient{
		configuration: config,
		rdbClient:     rdbClient,
	}
}

func (b BareClient) LastBlockFromUpstream() int {
	return b.rdbClient.GetLastBlockSynced()
}

func (b BareClient) RawHeadersOfBlock(blockHeight int) []byte {
	return b.rdbClient.GetHeaderForBlockLookupKey(b.rdbClient.GetLookupKeyForBlockHeight(blockHeight))
}

func (b BareClient) RawBodyOfBlock(blockHeight int) []byte {
	return b.rdbClient.GetBodyForBlockLookupKey(b.rdbClient.GetLookupKeyForBlockHeight(blockHeight))
}

func (b BareClient) StateRootKey(blockHeight int) string {
	headerDecoder := types.ScaleDecoder{}
	rawHeaderData := b.rdbClient.GetHeaderForBlockLookupKey(b.rdbClient.GetLookupKeyForBlockHeight(blockHeight))
	headerDecoder.Init(types.ScaleBytes{Data: rawHeaderData}, nil)
	decodedHeader := headerDecoder.ProcessAndUpdateData("Header")
	stateRootKey := getStateRootFromRawHeader(decodedHeader)
	return stateRootKey
}

func (b BareClient) RawEventOfBlock(blockHeight int) []byte {
	return b.ReadRawEvent(b.StateRootKey(blockHeight))
}

const triePathNibbleCount = 64

var (
	eventTriePathHexNibbles = []byte{
		0x2, 0x6, 0xa, 0xa, 0x3, 0x9, 0x4, 0xe, 0xe, 0xa, 0x5, 0x6, 0x3, 0x0, 0xe, 0x0, 0x7, 0xc, 0x4, 0x8, 0xa, 0xe, 0x0, 0xc, 0x9, 0x5, 0x5, 0x8, 0xc, 0xe, 0xf, 0x7, 0x8, 0x0, 0xd, 0x4, 0x1, 0xe, 0x5, 0xe, 0x1, 0x6, 0x0, 0x5, 0x6, 0x7, 0x6, 0x5, 0xb, 0xc, 0x8, 0x4, 0x6, 0x1, 0x8, 0x5, 0x1, 0x0, 0x7, 0x2, 0xc, 0x9, 0xd, 0x7,
	}
	eventTriePathBytes = []byte{
		0x26, 0xaa, 0x39, 0x4e, 0xea, 0x56, 0x30, 0xe0, 0x7c, 0x48, 0xae, 0x0c, 0x95, 0x58, 0xce, 0xf7, 0x80, 0xd4, 0x1e, 0x5e, 0x16, 0x05, 0x67, 0x65, 0xbc, 0x84, 0x61, 0x85, 0x10, 0x72, 0xc9, 0xd7,
	}
	nibbleToZeroPaddedByte = map[byte]byte{
		0x0: 0x00,
		0x1: 0x10,
		0x2: 0x20,
		0x3: 0x30,
		0x4: 0x40,
		0x5: 0x50,
		0x6: 0x60,
		0x7: 0x70,
		0x8: 0x80,
		0x9: 0x90,
		0xa: 0xa0,
		0xb: 0xb0,
		0xc: 0xc0,
		0xd: 0xd0,
		0xe: 0xe0,
		0xf: 0xf0,
	}
)

func (b BareClient) ReadRawEvent(rootStateKey string) []byte {
	var err error
	stateKey := make([]byte, 64)

	stateKey, err = hex.DecodeString(rootStateKey)
	if err != nil {
		panic(err)
	}

	nibbleCount := 0
	for nibbleCount != triePathNibbleCount {
		node := b.rdbClient.GetStateTrieNode(stateKey)
		decodedNode, err := trieNode.Decode(bytes.NewReader(node))
		if err != nil {
			panic(err)
		}

		switch decodedNode.Type() {
		case trieNode.BranchType:
			{
				decodedBranch := decodedNode.(*trieNode.Branch)

				// jump over the partial key
				nibbleCount += len(decodedBranch.Key)
				if nibbleCount == triePathNibbleCount {
					return decodedBranch.Value
				}

				childHash := decodedBranch.Children[eventTriePathHexNibbles[nibbleCount]].GetHash()
				nibbleCount++

				stateKey = append([]byte{}, eventTriePathBytes[:nibbleCount/2]...)
				if nibbleCount%2 == 1 {
					stateKey = append(stateKey, nibbleToZeroPaddedByte[eventTriePathHexNibbles[nibbleCount-1]])
				}
				stateKey = append(stateKey, childHash...)
			}
		case trieNode.LeafType:
			{
				decodedLeaf := decodedNode.(*trieNode.Leaf)
				return decodedLeaf.Value
			}
		}
	}
	return nil
}

func getEventCall(blockHeight int, decodedEvent map[string]interface{}) string {
	event, ok := decodedEvent["event_id"].(string)
	if !ok {
		messages.NewDictionaryMessage(
			messages.LOG_LEVEL_ERROR,
			messages.GetComponent(getEventCall),
			nil,
			"",
			"failed to get event call of %v",
			blockHeight,
		).ConsoleLog()
	}
	return event
}

const (
	extrinsicSuccessCall = "ExtrinsicSuccess"
	extrinsicFailedCall  = "ExtrinsicFailed"
)

func (b BareClient) EventOfBlock(blockHeight int) int {
	eventDecoder := scalecodec.EventsDecoder{}
	eventDecoderOption := types.ScaleDecoderOption{Metadata: nil, Spec: -1}
	rawEvents := b.RawEventOfBlock(blockHeight)
	fmt.Println("rawEvents")
	fmt.Println(rawEvents)
	specNum := b.GetSpecVersionFromUpstream(blockHeight)
	fmt.Println("specNum")
	fmt.Println(specNum)
	metadata := b.GetMetadataFromUpstream(blockHeight)
	eventDecoderOption.Metadata = &metadata
	eventDecoderOption.Spec = specNum

	c, err := ioutil.ReadFile(b.configuration.ChainConfig.DecoderTypesFile)
	if err != nil {
		messages.NewDictionaryMessage(
			messages.LOG_LEVEL_ERROR,
			"",
			err,
			"Failed to read type file",
		).ConsoleLog()
	}
	types.RuntimeType{}.Reg()
	types.RegCustomTypes(source.LoadTypeRegistry(c))

	eventDecoder.Init(types.ScaleBytes{Data: rawEvents}, &eventDecoderOption)
	eventDecoder.Process()

	eventsCounter := 0
	eventsArray := eventDecoder.Value.([]interface{})
	for _, evt := range eventsArray {
		evtValue := evt.(map[string]interface{})
		eventCall := getEventCall(blockHeight, evtValue)

		switch eventCall {
		case extrinsicSuccessCall:
			fmt.Println("extrinsicSuccessCall")
		case extrinsicFailedCall:
			fmt.Println("extrinsicFailedCall")
		default:
			fmt.Println("default")
			eventsCounter++
		}
	}

	fmt.Printf("eventsCounter : %v", eventsCounter)
	return eventsCounter
}

const (
	bodyTypeString = "Vec<Bytes>"
)

func (b BareClient) ExtrinsicOfBlock(blockHeight int) int {
	bodyDecoder := types.ScaleDecoder{}
	extrinsicDecoder := scalecodec.ExtrinsicDecoder{}
	extrinsicDecoderOption := types.ScaleDecoderOption{Metadata: nil, Spec: -1}
	rawBodyData := b.RawBodyOfBlock(blockHeight)
	bodyDecoder.Init(types.ScaleBytes{Data: rawBodyData}, nil)
	decodedBody := bodyDecoder.ProcessAndUpdateData(bodyTypeString)
	bodyList, ok := decodedBody.([]interface{})
	if !ok {
		messages.NewDictionaryMessage(
			messages.LOG_LEVEL_ERROR,
			"",
			nil,
			messages.FAILED_TYPE_ASSERTION,
		).ConsoleLog()
	}
	specNum := b.GetSpecVersionFromUpstream(blockHeight)
	metadata := b.GetMetadataFromUpstream(blockHeight)
	extrinsicDecoderOption.Metadata = &metadata
	extrinsicDecoderOption.Spec = specNum
	offset := 0
	reg4RawEx := ""
	defer func() {
		if err := recover(); err != nil {
			fmt.Println("idx: ", offset, "rawExtrinsic: ", reg4RawEx)
			panic(err)
		}
	}()
	fmt.Println("bodyList size: ", len(bodyList))
	for idx, bodyInterface := range bodyList {
		fmt.Println("idx:", idx)
		rawExtrinsic, ok := bodyInterface.(string)
		if !ok {
			messages.NewDictionaryMessage(
				messages.LOG_LEVEL_ERROR,
				"",
				nil,
				messages.FAILED_TYPE_ASSERTION,
			).ConsoleLog()
			extrinsicDecoder.Init(types.ScaleBytes{Data: utiles.HexToBytes(rawExtrinsic)}, &extrinsicDecoderOption)
			reg4RawEx = rawExtrinsic
			offset = idx
			extrinsicDecoder.Process()
		}
	}
	return 0
}

var (
	SPEC_VERSION_MESSAGE = `{"id":1,"method":"chain_getRuntimeVersion","params":["%s"],"jsonrpc":"2.0"}`
	hexPrefix            = "0x"
)

func (b BareClient) GetSpecVersionFromUpstream(blockHeight int) int {
	hash := b.rdbClient.GetBlockHash(blockHeight)
	msg := fmt.Sprintf(SPEC_VERSION_MESSAGE, hexPrefix+hash)
	reqBody := bytes.NewBuffer([]byte(msg))
	retryClient := retryablehttp.NewClient()
	retryClient.RetryWaitMin = 15 * time.Second
	resp, postErr := retryClient.Post(b.configuration.ChainConfig.HttpRpcEndpoint, "application/json", reqBody)
	if postErr != nil {
		messages.NewDictionaryMessage(
			messages.LOG_LEVEL_ERROR,
			"",
			postErr,
			"Failed to get specv for block %v",
			blockHeight,
		).ConsoleLog()
	}

	v := &rpc.JsonRpcResult{}
	jsonDecodeErr := json.NewDecoder(resp.Body).Decode(&v)
	if jsonDecodeErr != nil {
		messages.NewDictionaryMessage(
			messages.LOG_LEVEL_ERROR,
			"",
			jsonDecodeErr,
			"failed to decode json of reply from rpc specv",
			blockHeight,
		).ConsoleLog()
	}

	return v.ToRuntimeVersion().SpecVersion

}

func (b BareClient) GetMetadataFromUpstream(blockHeight int) types.MetadataStruct {
	hash := b.rdbClient.GetBlockHash(blockHeight)
	reqBody := bytes.NewBuffer([]byte(rpc.StateGetMetadata(1, "0x"+hash)))
	retryClient := retryablehttp.NewClient()
	retryClient.RetryWaitMin = 15 * time.Second
	// resp, err := retryClient.Post(b.specVClient.HttpEndpoint(), "application/json", reqBody)
	resp, err := retryClient.Post(b.configuration.ChainConfig.HttpRpcEndpoint, "application/json", reqBody)
	if err != nil {
		messages.NewDictionaryMessage(
			messages.LOG_LEVEL_ERROR,
			"",
			err,
			"Failed to get metadata of %v",
			blockHeight,
		).ConsoleLog()
	}
	defer resp.Body.Close()

	metaRawBody := &rpc.JsonRpcResult{}
	err = json.NewDecoder(resp.Body).Decode(metaRawBody)
	if err != nil {
		messages.NewDictionaryMessage(
			messages.LOG_LEVEL_ERROR,
			"",
			err,
			"Failed to decode block %v",
			blockHeight,
		).ConsoleLog()
	}
	metaBodyString, err := metaRawBody.ToString()
	fmt.Println("metaBodyString")
	fmt.Println(metaBodyString)
	if err != nil {
		messages.NewDictionaryMessage(
			messages.LOG_LEVEL_ERROR,
			"",
			err,
			"Failed to decode block %v",
			blockHeight,
		).ConsoleLog()
	}

	// specVClient.metadataDecoder.Init(utiles.HexToBytes(metaBodyString))
	// err = specVClient.metadataDecoder.Process()
	metadataDecoder := scalecodec.MetadataDecoder{}
	metadataDecoder.Init(utiles.HexToBytes(metaBodyString))
	err = metadataDecoder.Process()
	if err != nil {
		messages.NewDictionaryMessage(
			messages.LOG_LEVEL_ERROR,
			"",
			err,
			"Metadata Decoder Fail",
			blockHeight,
		).ConsoleLog()
	}
	return metadataDecoder.Metadata
}

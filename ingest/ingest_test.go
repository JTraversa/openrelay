package ingest_test

import (
	"bytes"
	"encoding/hex"
	accountsModule "github.com/notegio/openrelay/accounts"
	affiliatesModule "github.com/notegio/openrelay/affiliates"
	"github.com/notegio/openrelay/common"
	"github.com/notegio/openrelay/ingest"
	"github.com/notegio/openrelay/types"
	poolModule "github.com/notegio/openrelay/pool"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"errors"
	"io"
)

var (
	feeTokenAsset, _ = common.HexToAssetData("f47261b00000000000000000000000006b175474e89094c44da98b954eedeac495271d0f")
)

func mockPoolDecoratorFee(fee *big.Int, fn func(http.ResponseWriter, *http.Request, *poolModule.Pool)) func(http.ResponseWriter, *http.Request) {
	baseFee := &mockBaseFee{fee}
	return func(w http.ResponseWriter, r *http.Request) {
		sender, _:= common.HexToAddress("0x0000000000000000000000000000000000000000")
		pool := &poolModule.Pool{SearchTerms: "", ID: []byte("default"), SenderAddresses: types.NetworkAddressMap{1: sender}, FeeTokenAddress: types.NetworkAddressMap{1: sender}}
		pool.SetBaseFee(baseFee)
		fn(w, r, pool)
	}
}

type TestPublisher struct {
	channel  string
	messages []string
}

func (pub *TestPublisher) Publish(message string) bool {
	pub.messages = append(pub.messages, message)
	return true
}

type TestAccount struct {
	blacklist bool
	discount  *big.Int
}

func (acct *TestAccount) Blacklisted() bool {
	return acct.blacklist
}
func (acct *TestAccount) Discount() *big.Int {
	return acct.discount
}

type TestAffiliate struct {
	fee *big.Int
}

func (affiliate *TestAffiliate) Fee() *big.Int {
	return affiliate.fee
}

type TestAffiliateService struct {
	fee *big.Int
	err error
}

func (service *TestAffiliateService) Get(address *types.Address) (affiliatesModule.Affiliate, error) {
	if service.err == nil {
		return &TestAffiliate{service.fee}, nil
	}
	return nil, service.err
}

// Set must be provided to satisfy the interface, but we don't need it for these tests.
func (service *TestAffiliateService) Set(address *types.Address, affiliate affiliatesModule.Affiliate) error {
	return nil
}

func (service *TestAffiliateService) List() ([]types.Address, error) {
	return []types.Address{}, nil
}

type TestAccountService struct {
	blacklist bool
	discount  *big.Int
}

type TestReader struct {
	reader io.Reader
	err  error
}

func (reader TestReader) Read(p []byte) (n int, err error) {
	if reader.err != nil {
		return 0, reader.err
	}
	return reader.reader.Read(p)
}

// Get makes up an account deterministically based on the provided address
func (service *TestAccountService) Get(address *types.Address) accountsModule.Account {
	account := &TestAccount{
		service.blacklist,
		service.discount,
	}
	return account
}

// Set must be provided to satisfy the interface, but we don't need it for these tests.
func (service *TestAccountService) Set(address *types.Address, account accountsModule.Account) error {
	return nil
}

type TestTermsManager struct {
	result bool
}

func (tm *TestTermsManager) CheckAddress(*types.Address) (<-chan bool) {
	result := make(chan bool)
	go func() {result <- tm.result}()
	return result
}

type TestExchangeLookup struct {
	result uint
}
func (tm *TestExchangeLookup) ExchangeIsKnown(*types.Address) (<-chan uint) {
	result := make(chan uint)
	go func() {result <- tm.result}()
	return result
}

func TestBadRead(t *testing.T) {
	publisher := TestPublisher{}
	handler := mockPoolDecorator(ingest.Handler(&publisher, &TestAccountService{}, &TestAffiliateService{}, true, &TestTermsManager{true}, &TestExchangeLookup{1}))
	reader := TestReader{
		bytes.NewReader([]byte("00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000")),
		errors.New("Fail!"),
	}
	request, _ := http.NewRequest("POST", "/", reader)
	request.Header["Content-Type"] = []string{"application/octet-stream"}
	recorder := httptest.NewRecorder()
	handler(recorder, request)
	if recorder.Code != 500 {
		t.Errorf("Expected error code 500, got '%v'", recorder.Code)
	}
	if recorder.HeaderMap["Content-Type"][0] != "application/json" {
		t.Errorf("Got unexpected content type: %v", recorder.HeaderMap["Content-Type"])
	}
	body := recorder.Body.String()
	if body != "{\"code\":100,\"reason\":\"Error reading content\"}" {
		t.Errorf("Got unexpected body: '%v' - %v", body, len(body))
	}
	if len(publisher.messages) != 0 {
		t.Errorf("Unexpected message count '%v'", len(publisher.messages))
	}
}
func TestBadJSON(t *testing.T) {
	publisher := TestPublisher{}
	handler := mockPoolDecorator(ingest.Handler(&publisher, &TestAccountService{}, &TestAffiliateService{}, true, &TestTermsManager{true}, &TestExchangeLookup{1}))
	reader := TestReader{
		bytes.NewReader([]byte("bad json")),
		nil,
	}
	request, _ := http.NewRequest("POST", "/", reader)
	request.Header["Content-Type"] = []string{"application/json"}
	recorder := httptest.NewRecorder()
	handler(recorder, request)
	if recorder.Code != 400 {
		t.Errorf("Expected error code 400, got '%v'", recorder.Code)
	}
	if contentType, ok := recorder.HeaderMap["Content-Type"]; !ok || contentType[0] != "application/json" {
		t.Errorf("Got unexpected content type %v", contentType)
	}
	body := recorder.Body.String()
	if body != "{\"code\":101,\"reason\":\"Malformed JSON\"}" {
		t.Errorf("Got unexpected body: '%v' - %v", body, len(body))
	}
	if len(publisher.messages) != 0 {
		t.Errorf("Unexpected message count '%v'", len(publisher.messages))
	}
}
func TestJSONBadRead(t *testing.T) {
	publisher := TestPublisher{}
	handler := mockPoolDecorator(ingest.Handler(&publisher, &TestAccountService{}, &TestAffiliateService{}, true, &TestTermsManager{true}, &TestExchangeLookup{1}))
	reader := TestReader{
		bytes.NewReader([]byte("bad json")),
		errors.New("Sample Error"),
	}
	request, _ := http.NewRequest("POST", "/", reader)
	request.Header["Content-Type"] = []string{"application/json"}
	recorder := httptest.NewRecorder()
	handler(recorder, request)
	if recorder.Code != 500 {
		t.Errorf("Expected error code 500, got '%v'", recorder.Code)
	}
	if contentType, ok := recorder.HeaderMap["Content-Type"]; !ok || contentType[0] != "application/json" {
		t.Errorf("Got unexpected content type %v", contentType)
	}
	body := recorder.Body.String()
	if body != "{\"code\":100,\"reason\":\"Error reading content\"}" {
		t.Errorf("Got unexpected body: '%v' - %v", body, len(body))
	}
	if len(publisher.messages) != 0 {
		t.Errorf("Unexpected message count '%v'", len(publisher.messages))
	}
}
func TestNoContentType(t *testing.T) {
	publisher := TestPublisher{}
	handler := mockPoolDecorator(ingest.Handler(&publisher, &TestAccountService{}, &TestAffiliateService{}, true, &TestTermsManager{true}, &TestExchangeLookup{1}))
	reader := TestReader{
		bytes.NewReader([]byte("")),
		nil,
	}
	request, _ := http.NewRequest("POST", "/", reader)
	recorder := httptest.NewRecorder()
	handler(recorder, request)
	if recorder.Code != 415 {
		t.Errorf("Expected error code 400, got '%v'", recorder.Code)
	}
	if contentType, ok := recorder.HeaderMap["Content-Type"]; !ok || contentType[0] != "application/json" {
		t.Errorf("Got unexpected content type %v", contentType)
	}
	body := recorder.Body.String()
	if body != "{\"code\":100,\"reason\":\"Unsupported content-type\"}" {
		t.Errorf("Got unexpected body: '%v' - %v", body, len(body))
	}
	if len(publisher.messages) != 0 {
		t.Errorf("Unexpected message count '%v'", len(publisher.messages))
	}
}
func TestBadSignature(t *testing.T) {
	publisher := TestPublisher{}
	handler := mockPoolDecorator(ingest.Handler(&publisher, &TestAccountService{}, &TestAffiliateService{}, true, &TestTermsManager{true}, &TestExchangeLookup{1}))
	data, _ := hex.DecodeString("f9022d94d60c1c164ec575f6572f99302331e061eff3c7b7940000000000000000000000000000000000000000941dad4783cf3fe3085c1426157ab175a6119a04ba9405d090b51c40b020eab3bfcb6a2dff130df22e9ca4f47261b00000000000000000000000001dad4783cf3fe3085c1426157ab175a6119a04baa4f47261b000000000000000000000000005d090b51c40b020eab3bfcb6a2dff130df22e9c9400000000000000000000000000000000000000009490fe2af704b34e0224bf2299c838e04d4dcf1364940000000000000000000000000000000000000000a0000000000000000000000000000000000000000000000002b5e3af16b1880000a00000000000000000000000000000000000000000000000000de0b6b3a7640000a00000000000000000000000000000000000000000000000000000000000000000a000000000000000000000000000000000000000000000000000000000000000008080a00000000000000000000000000000000000000000000000000000000159938ac4a0000643508ff7019bfb134363a86e98746f6c33262e68daf992b8df064217222bb8421c5aa36ecbdcd5ee3f8557cfe4dd8bd34a1f4e11b4a6731f215d1e184eaa058e32210ba77921ce26bb03da4af4b81cc1e7a91b39362e8f7d5e64af7dccfa79eb1a03a000000000000000000000000000000000000000000000000000000000000000008080a00000000000000000000000000000000000000000000000000000000000000001")
	reader := TestReader{
		bytes.NewReader(data),
		nil,
	}
	request, _ := http.NewRequest("POST", "/", reader)
	request.Header["Content-Type"] = []string{"application/octet-stream"}
	recorder := httptest.NewRecorder()
	handler(recorder, request)
	if recorder.Code != 400 {
		t.Errorf("Expected error code 400, got '%v'", recorder.Code)
	}
	if contentType, ok := recorder.HeaderMap["Content-Type"]; !ok || contentType[0] != "application/json" {
		t.Errorf("Got unexpected content type %v", contentType)
	}
	if body := recorder.Body.String(); body != "{\"code\":100,\"reason\":\"Validation Failed\",\"validationErrors\":[{\"field\":\"signature\",\"code\":1005,\"reason\":\"Signature validation failed\"}]}" {
		t.Errorf("Got unexpected body: '%v' - %v", body, len(body))
	}
	if len(publisher.messages) != 0 {
		t.Errorf("Unexpected message count '%v'", len(publisher.messages))
	}
}
func TestInsufficientFee(t *testing.T) {
	publisher := TestPublisher{}
	fee := new(big.Int)
	fee.SetInt64(1000)
	handler := mockPoolDecoratorFee(fee, ingest.Handler(&publisher, &TestAccountService{false, new(big.Int)}, &TestAffiliateService{fee, nil}, true, &TestTermsManager{true}, &TestExchangeLookup{1}))
	data, _ := hex.DecodeString("f9022d94d60c1c164ec575f6572f99302331e061eff3c7b7940000000000000000000000000000000000000000941dad4783cf3fe3085c1426157ab175a6119a04ba9405d090b51c40b020eab3bfcb6a2dff130df22e9ca4f47261b00000000000000000000000001dad4783cf3fe3085c1426157ab175a6119a04baa4f47261b000000000000000000000000005d090b51c40b020eab3bfcb6a2dff130df22e9c9400000000000000000000000000000000000000009490fe2af704b34e0224bf2299c838e04d4dcf1364940000000000000000000000000000000000000000a0000000000000000000000000000000000000000000000002b5e3af16b1880000a00000000000000000000000000000000000000000000000000de0b6b3a7640000a00000000000000000000000000000000000000000000000000000000000000000a000000000000000000000000000000000000000000000000000000000000000008080a00000000000000000000000000000000000000000000000000000000159938ac4a0000643508ff7019bfb134363a86e98746f6c33262e68daf992b8df064217222bb8421c5aa36ecbdcd5ee3f8557cfe4dd8bd34a1f4e11b4a6731f215d1e184eaa058e32210ba77921ce26bb03da4af4b81cc1e7a91b39362e8f7d5e64af7dccfa79eb1f03a000000000000000000000000000000000000000000000000000000000000000008080a00000000000000000000000000000000000000000000000000000000000000001")
	reader := TestReader{
		bytes.NewReader(data),
		nil,
	}
	request, _ := http.NewRequest("POST", "/", reader)
	request.Header["Content-Type"] = []string{"application/octet-stream"}
	recorder := httptest.NewRecorder()
	handler(recorder, request)
	if recorder.Code != 402 {
		t.Errorf("Expected error code 402, got '%v'", recorder.Code)
	}
	if contentType, ok := recorder.HeaderMap["Content-Type"]; !ok || contentType[0] != "application/json" {
		t.Errorf("Got unexpected content type %v", contentType)
	}
	body := recorder.Body.String()
	if body != "{\"code\":100,\"reason\":\"Validation Failed\",\"validationErrors\":[{\"field\":\"makerFee\",\"code\":1004,\"reason\":\"Total fee must be at least: 1000\"},{\"field\":\"takerFee\",\"code\":1004,\"reason\":\"Total fee must be at least: 1000\"}]}" {
		t.Errorf("Got unexpected body: '%v' - %v", body, len(body))
	}
	if len(publisher.messages) != 0 {
		t.Errorf("Unexpected message count '%v'", len(publisher.messages))
	}
}
func TestBlacklisted(t *testing.T) {
	publisher := TestPublisher{}
	handler := mockPoolDecorator(ingest.Handler(&publisher, &TestAccountService{true, new(big.Int)}, &TestAffiliateService{new(big.Int), nil}, true, &TestTermsManager{true}, &TestExchangeLookup{1}))
	data, _ := hex.DecodeString("f9022d94d60c1c164ec575f6572f99302331e061eff3c7b7940000000000000000000000000000000000000000941dad4783cf3fe3085c1426157ab175a6119a04ba9405d090b51c40b020eab3bfcb6a2dff130df22e9ca4f47261b00000000000000000000000001dad4783cf3fe3085c1426157ab175a6119a04baa4f47261b000000000000000000000000005d090b51c40b020eab3bfcb6a2dff130df22e9c9400000000000000000000000000000000000000009490fe2af704b34e0224bf2299c838e04d4dcf1364940000000000000000000000000000000000000000a0000000000000000000000000000000000000000000000002b5e3af16b1880000a00000000000000000000000000000000000000000000000000de0b6b3a7640000a00000000000000000000000000000000000000000000000000000000000000000a000000000000000000000000000000000000000000000000000000000000000008080a00000000000000000000000000000000000000000000000000000000159938ac4a0000643508ff7019bfb134363a86e98746f6c33262e68daf992b8df064217222bb8421c5aa36ecbdcd5ee3f8557cfe4dd8bd34a1f4e11b4a6731f215d1e184eaa058e32210ba77921ce26bb03da4af4b81cc1e7a91b39362e8f7d5e64af7dccfa79eb1f03a000000000000000000000000000000000000000000000000000000000000000008080a00000000000000000000000000000000000000000000000000000000000000001")
	reader := TestReader{
		bytes.NewReader(data),
		nil,
	}
	request, _ := http.NewRequest("POST", "/", reader)
	request.Header["Content-Type"] = []string{"application/octet-stream"}
	recorder := httptest.NewRecorder()
	handler(recorder, request)
	if recorder.Code != 202 {
		t.Errorf("Expected code 202, got '%v'", recorder.Code)
		t.Errorf("Body: '%v'", recorder.Body.String())
	}
	if len(publisher.messages) != 0 {
		t.Errorf("Unexpected message count '%v'", len(publisher.messages))
	}
}
func TestNotFeeRecipient(t *testing.T) {
	publisher := TestPublisher{}
	handler := mockPoolDecorator(ingest.Handler(&publisher, &TestAccountService{true, new(big.Int)}, &TestAffiliateService{nil, errors.New("Fee Recipient must be an authorized address")}, true, &TestTermsManager{true}, &TestExchangeLookup{1}))
	data, _ := hex.DecodeString("f9022d94d60c1c164ec575f6572f99302331e061eff3c7b7940000000000000000000000000000000000000000941dad4783cf3fe3085c1426157ab175a6119a04ba9405d090b51c40b020eab3bfcb6a2dff130df22e9ca4f47261b00000000000000000000000001dad4783cf3fe3085c1426157ab175a6119a04baa4f47261b000000000000000000000000005d090b51c40b020eab3bfcb6a2dff130df22e9c9400000000000000000000000000000000000000009490fe2af704b34e0224bf2299c838e04d4dcf1364940000000000000000000000000000000000000000a0000000000000000000000000000000000000000000000002b5e3af16b1880000a00000000000000000000000000000000000000000000000000de0b6b3a7640000a00000000000000000000000000000000000000000000000000000000000000000a000000000000000000000000000000000000000000000000000000000000000008080a00000000000000000000000000000000000000000000000000000000159938ac4a0000643508ff7019bfb134363a86e98746f6c33262e68daf992b8df064217222bb8421c5aa36ecbdcd5ee3f8557cfe4dd8bd34a1f4e11b4a6731f215d1e184eaa058e32210ba77921ce26bb03da4af4b81cc1e7a91b39362e8f7d5e64af7dccfa79eb1f03a000000000000000000000000000000000000000000000000000000000000000008080a00000000000000000000000000000000000000000000000000000000000000001")
	reader := TestReader{
		bytes.NewReader(data),
		nil,
	}
	request, _ := http.NewRequest("POST", "/", reader)
	request.Header["Content-Type"] = []string{"application/octet-stream"}
	recorder := httptest.NewRecorder()
	handler(recorder, request)
	if recorder.Code != 402 {
		t.Errorf("Expected error code 402, got '%v'", recorder.Code)
	}
	body := recorder.Body.String()
	if body != "{\"code\":100,\"reason\":\"Validation Failed\",\"validationErrors\":[{\"field\":\"feeRecipient\",\"code\":1002,\"reason\":\"Invalid fee recipient\"}]}" {
		t.Errorf("Got unexpected body: '%v' - %v", body, len(body))
	}
	if len(publisher.messages) != 0 {
		t.Errorf("Unexpected message count '%v'", len(publisher.messages))
	}
}
func TestValid(t *testing.T) {
	publisher := TestPublisher{}
	handler := mockPoolDecorator(ingest.Handler(&publisher, &TestAccountService{false, new(big.Int)}, &TestAffiliateService{new(big.Int), nil}, true, &TestTermsManager{true}, &TestExchangeLookup{1}))
	data, _ := hex.DecodeString("f9023494d60c1c164ec575f6572f99302331e061eff3c7b7940000000000000000000000000000000000000000941dad4783cf3fe3085c1426157ab175a6119a04ba9405d090b51c40b020eab3bfcb6a2dff130df22e9ca4f47261b00000000000000000000000001dad4783cf3fe3085c1426157ab175a6119a04baa4f47261b000000000000000000000000005d090b51c40b020eab3bfcb6a2dff130df22e9c9400000000000000000000000000000000000000009490fe2af704b34e0224bf2299c838e04d4dcf1364940000000000000000000000000000000000000000a0000000000000000000000000000000000000000000000002b5e3af16b1880000a00000000000000000000000000000000000000000000000000de0b6b3a7640000a00000000000000000000000000000000000000000000000000000000000000000a000000000000000000000000000000000000000000000000000000000000000008080a00000000000000000000000000000000000000000000000000000000159938ac4a0000643508ff7019bfb134363a86e98746f6c33262e68daf992b8df064217222bb8421c5aa36ecbdcd5ee3f8557cfe4dd8bd34a1f4e11b4a6731f215d1e184eaa058e32210ba77921ce26bb03da4af4b81cc1e7a91b39362e8f7d5e64af7dccfa79eb1f03a00000000000000000000000000000000000000000000000000000000000000000808764656661756c74a00000000000000000000000000000000000000000000000000000000000000001")
	reader := TestReader{
		bytes.NewReader(data),
		nil,
	}
	request, _ := http.NewRequest("POST", "/", reader)
	request.Header["Content-Type"] = []string{"application/octet-stream"}
	recorder := httptest.NewRecorder()
	handler(recorder, request)
	if recorder.Code != 202 {
		t.Errorf("Expected error code 202, got '%v'", recorder.Code)
		t.Errorf("Body: '%v'", recorder.Body.String())
	}
	if len(publisher.messages) != 1 {
		t.Errorf("Unexpected message count '%v'", len(publisher.messages))
		return
	}
	if publisher.messages[0] != string(data) {
		t.Errorf("Unexpected message data: %#x", publisher.messages[0])
	}
}
func TestBadExchange(t *testing.T) {
	publisher := TestPublisher{}
	handler := mockPoolDecorator(ingest.Handler(&publisher, &TestAccountService{false, new(big.Int)}, &TestAffiliateService{new(big.Int), nil}, true, &TestTermsManager{true}, &TestExchangeLookup{0}))
	data, _ := hex.DecodeString("f9022d94d60c1c164ec575f6572f99302331e061eff3c7b7940000000000000000000000000000000000000000941dad4783cf3fe3085c1426157ab175a6119a04ba9405d090b51c40b020eab3bfcb6a2dff130df22e9ca4f47261b00000000000000000000000001dad4783cf3fe3085c1426157ab175a6119a04baa4f47261b000000000000000000000000005d090b51c40b020eab3bfcb6a2dff130df22e9c9400000000000000000000000000000000000000009490fe2af704b34e0224bf2299c838e04d4dcf1364940000000000000000000000000000000000000000a0000000000000000000000000000000000000000000000002b5e3af16b1880000a00000000000000000000000000000000000000000000000000de0b6b3a7640000a00000000000000000000000000000000000000000000000000000000000000000a000000000000000000000000000000000000000000000000000000000000000008080a00000000000000000000000000000000000000000000000000000000159938ac4a0000643508ff7019bfb134363a86e98746f6c33262e68daf992b8df064217222bb8421c5aa36ecbdcd5ee3f8557cfe4dd8bd34a1f4e11b4a6731f215d1e184eaa058e32210ba77921ce26bb03da4af4b81cc1e7a91b39362e8f7d5e64af7dccfa79eb1f03a000000000000000000000000000000000000000000000000000000000000000008080a00000000000000000000000000000000000000000000000000000000000000001")
	reader := TestReader{
		bytes.NewReader(data),
		nil,
	}
	request, _ := http.NewRequest("POST", "/", reader)
	request.Header["Content-Type"] = []string{"application/octet-stream"}
	recorder := httptest.NewRecorder()
	handler(recorder, request)
	if recorder.Code != 400 {
		t.Errorf("Expected error code 400, got '%v'", recorder.Code)
	}
	body := recorder.Body.String()
	if body != "{\"code\":100,\"reason\":\"Validation Failed\",\"validationErrors\":[{\"field\":\"exchangeContractAddress\",\"code\":1002,\"reason\":\"Unknown exchangeContractAddress\"}]}" {
		t.Errorf("Got unexpected body: '%v' - %v", body, len(body))
	}
	if len(publisher.messages) != 0 {
		t.Errorf("Unexpected message count '%v'", len(publisher.messages))
		return
	}
}
func TestUnsignedMaker(t *testing.T) {
	publisher := TestPublisher{}
	handler := mockPoolDecorator(ingest.Handler(&publisher, &TestAccountService{false, new(big.Int)}, &TestAffiliateService{new(big.Int), nil}, true, &TestTermsManager{false}, &TestExchangeLookup{1}))
	data, _ := hex.DecodeString("f9022d94d60c1c164ec575f6572f99302331e061eff3c7b7940000000000000000000000000000000000000000941dad4783cf3fe3085c1426157ab175a6119a04ba9405d090b51c40b020eab3bfcb6a2dff130df22e9ca4f47261b00000000000000000000000001dad4783cf3fe3085c1426157ab175a6119a04baa4f47261b000000000000000000000000005d090b51c40b020eab3bfcb6a2dff130df22e9c9400000000000000000000000000000000000000009490fe2af704b34e0224bf2299c838e04d4dcf1364940000000000000000000000000000000000000000a0000000000000000000000000000000000000000000000002b5e3af16b1880000a00000000000000000000000000000000000000000000000000de0b6b3a7640000a00000000000000000000000000000000000000000000000000000000000000000a000000000000000000000000000000000000000000000000000000000000000008080a00000000000000000000000000000000000000000000000000000000159938ac4a0000643508ff7019bfb134363a86e98746f6c33262e68daf992b8df064217222bb8421c5aa36ecbdcd5ee3f8557cfe4dd8bd34a1f4e11b4a6731f215d1e184eaa058e32210ba77921ce26bb03da4af4b81cc1e7a91b39362e8f7d5e64af7dccfa79eb1f03a000000000000000000000000000000000000000000000000000000000000000008080a00000000000000000000000000000000000000000000000000000000000000001")
	reader := TestReader{
		bytes.NewReader(data),
		nil,
	}
	request, _ := http.NewRequest("POST", "/", reader)
	request.Header["Content-Type"] = []string{"application/octet-stream"}
	recorder := httptest.NewRecorder()
	handler(recorder, request)
	if recorder.Code != 401 {
		t.Errorf("Expected error code 401, got '%v'", recorder.Code)
	}
	body := recorder.Body.String()
	if body != "{\"code\":100,\"reason\":\"Validation Failed\",\"validationErrors\":[{\"field\":\"makerAddress\",\"code\":1002,\"reason\":\"makerAddress must sign terms of service\"}]}" {
		t.Errorf("Got unexpected body: '%v' - %v", body, len(body))
	}
	if len(publisher.messages) != 0 {
		t.Errorf("Unexpected message count '%v'", len(publisher.messages))
		return
	}
}

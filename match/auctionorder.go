package match

import (
	"bytes"
	"encoding/binary"
	"encoding/gob"
	"encoding/json"
	"fmt"

	"github.com/mit-dci/opencx/crypto"
	"github.com/mit-dci/opencx/crypto/hashtimelock"
	"github.com/mit-dci/opencx/crypto/rsw"
	"github.com/mit-dci/opencx/crypto/timelockencoders"
)

// EncryptedAuctionOrder represents an encrypted Auction Order, so a ciphertext and a puzzle whos solution is a key, and an intended auction.
type EncryptedAuctionOrder struct {
	OrderCiphertext []byte
	OrderPuzzle     crypto.Puzzle
	IntendedAuction [32]byte
}

// SolveRC5AuctionOrderAsync solves order puzzles and creates auction orders from them. This should be run in a goroutine.
func SolveRC5AuctionOrderAsync(e *EncryptedAuctionOrder, puzzleResChan chan *OrderPuzzleResult) {
	var err error
	result := new(OrderPuzzleResult)
	result.Encrypted = e

	var orderBytes []byte
	if orderBytes, err = timelockencoders.SolvePuzzleRC5(e.OrderCiphertext, e.OrderPuzzle); err != nil {
		result.Err = fmt.Errorf("Error solving RC5 puzzle for auction order: %s", err)
		puzzleResChan <- result
		return
	}

	result.Auction = new(AuctionOrder)
	if err = result.Auction.Deserialize(orderBytes); err != nil {
		result.Err = fmt.Errorf("Error deserializing order gotten from puzzle: %s", err)
		puzzleResChan <- result
		return
	}

	puzzleResChan <- result

	return
}

// Serialize serializes the encrypted order using gob
func (e *EncryptedAuctionOrder) Serialize() (raw []byte, err error) {
	var b bytes.Buffer

	// register the rsw puzzle and hashtimelock puzzle
	gob.Register(new(rsw.PuzzleRSW))

	// register the hashtimelock (puzzle and timelock are same thing)
	gob.Register(new(hashtimelock.HashTimelock))

	// register the puzzle interface
	gob.RegisterName("puzzle", new(crypto.Puzzle))

	// register the encrypted auction order interface with gob
	gob.RegisterName("order", new(EncryptedAuctionOrder))

	// create a new encoder writing to our buffer
	enc := gob.NewEncoder(&b)

	// encode the encrypted auction order in the buffer
	if err = enc.Encode(e); err != nil {
		err = fmt.Errorf("Error encoding encrypted auction order :%s", err)
		return
	}

	// Get the bytes finally
	raw = b.Bytes()

	return
}

// Deserialize deserializes the raw bytes into the encrypted auction order receiver
func (e *EncryptedAuctionOrder) Deserialize(raw []byte) (err error) {
	var b *bytes.Buffer
	b = bytes.NewBuffer(raw)

	// register the rsw puzzle and hashtimelock puzzle
	gob.Register(new(rsw.PuzzleRSW))

	// register the hashtimelock (puzzle and timelock are same thing)
	gob.Register(new(hashtimelock.HashTimelock))

	// register the puzzle interface
	gob.RegisterName("puzzle", new(crypto.Puzzle))

	// register the encrypted auction order interface with gob
	gob.RegisterName("order", new(EncryptedAuctionOrder))

	// create a new decoder writing to the buffer
	dec := gob.NewDecoder(b)

	// decode the encrypted auction order in the buffer
	if err = dec.Decode(e); err != nil {
		err = fmt.Errorf("Error decoding encrypted auction order: %s", err)
		return
	}

	return
}

// OrderPuzzleResult is a struct that is used as the type for a channel so we can atomically
// receive the original encrypted order, decrypted order, and an error
type OrderPuzzleResult struct {
	Encrypted *EncryptedAuctionOrder
	Auction   *AuctionOrder
	Err       error
}

// AuctionOrder represents a batch order
type AuctionOrder struct {
	Pubkey      [33]byte `json:"pubkey"`
	Side        string   `json:"side"`
	TradingPair Pair     `json:"pair"`
	// amount of assetHave the user would like to trade
	AmountHave uint64 `json:"amounthave"`
	// amount of assetWant the user wants for their assetHave
	AmountWant uint64 `json:"amountwant"`
	// only exists for returning orders back
	OrderbookPrice float64 `json:"orderbookprice"`
	// IntendedAuction as the auctionID this should be in. We need this to protect against
	// the exchange withholding an order.
	AuctionID [32]byte `json:"auctionid"`
	// 2 byte nonce (So there can be max 2^16 of the same-looking orders by the same pubkey in the same batch)
	// This is used to protect against the exchange trying to replay a bunch of orders
	Nonce     [2]byte `json:"nonce"`
	Signature []byte  `json:"signature"`
}

// TurnIntoEncryptedOrder creates a puzzle for this auction order given the time. We make no assumptions about whether or not the order is signed.
func (a *AuctionOrder) TurnIntoEncryptedOrder(t uint64) (encrypted *EncryptedAuctionOrder, err error) {
	encrypted = new(EncryptedAuctionOrder)
	if encrypted.OrderCiphertext, encrypted.OrderPuzzle, err = timelockencoders.CreateRSW2048A2PuzzleRC5(t, a.Serialize()); err != nil {
		err = fmt.Errorf("Error creating puzzle from auction order: %s", err)
		return
	}
	// make sure they match
	encrypted.IntendedAuction = a.AuctionID
	return
}

// IsBuySide returns true if the limit order is buying
func (a *AuctionOrder) IsBuySide() bool {
	return a.Side == "buy"
}

// IsSellSide returns true if the limit order is selling
func (a *AuctionOrder) IsSellSide() bool {
	return a.Side == "sell"
}

// OppositeSide is a helper to get the opposite side of the order
func (a *AuctionOrder) OppositeSide() (sideStr string) {
	if a.IsBuySide() {
		sideStr = "sell"
	} else if a.IsSellSide() {
		sideStr = "buy"
	}
	return
}

// Price gets a float price for the order. This determines how it will get matched. The exchange should figure out if it can take some of the
func (a *AuctionOrder) Price() (price float64, err error) {
	if a.AmountWant == 0 {
		err = fmt.Errorf("The amount requested in the order is 0, so no price can be calculated. Consider it a donation")
		return
	}
	if a.IsBuySide() {
		price = float64(a.AmountWant) / float64(a.AmountHave)
		return
	} else if a.IsSellSide() {
		price = float64(a.AmountHave) / float64(a.AmountWant)
	}
	err = fmt.Errorf("Order is not buy or sell, cannot calculate price")
	return
}

// Serialize serializes an order, possible replay attacks here since this is what you're signing?
// but anyways this is the order: [33 byte pubkey] pair amountHave amountWant <length side> side [32 byte auctionid]
func (a *AuctionOrder) Serialize() (buf []byte) {
	// serializable fields:
	// public key (compressed) [33 bytes]
	// trading pair [2 bytes]
	// amounthave [8 bytes]
	// amountwant [8 bytes]
	// len side [8 bytes]
	// side [len side]
	// auctionID [32 bytes]
	// nonce [2 bytes]
	// len sig [8 bytes]
	// sig [len sig bytes]
	buf = append(buf, a.Pubkey[:]...)
	buf = append(buf, a.TradingPair.Serialize()...)

	amountHaveBytes := make([]byte, 8)
	binary.LittleEndian.PutUint64(amountHaveBytes, a.AmountHave)
	buf = append(buf, amountHaveBytes[:]...)

	amountWantBytes := make([]byte, 8)
	binary.LittleEndian.PutUint64(amountWantBytes, a.AmountWant)
	buf = append(buf, amountWantBytes[:]...)

	lenSideBytes := make([]byte, 8)
	binary.LittleEndian.PutUint64(lenSideBytes, uint64(len(a.Side)))
	buf = append(buf, lenSideBytes[:]...)

	buf = append(buf, []byte(a.Side)...)
	buf = append(buf, a.AuctionID[:]...)
	buf = append(buf, a.Nonce[:]...)

	lenSigBytes := make([]byte, 8)
	binary.LittleEndian.PutUint64(lenSigBytes, uint64(len(a.Signature)))
	buf = append(buf, lenSigBytes[:]...)

	buf = append(buf, a.Signature[:]...)
	return
}

// SerializeSignable serializes the fields that are hashable, and will be signed. These are also
// what would get verified.
func (a *AuctionOrder) SerializeSignable() (buf []byte) {
	// serializable fields:
	// public key (compressed) [33 bytes]
	// trading pair [2 bytes]
	// amounthave [8 bytes]
	// amountwant [8 bytes]
	// len side [8 bytes]
	// side [len side]
	// auctionID [32 bytes]
	// nonce [2 bytes]
	buf = append(buf, a.Pubkey[:]...)
	buf = append(buf, a.TradingPair.Serialize()...)

	amountHaveBytes := make([]byte, 8)
	binary.LittleEndian.PutUint64(amountHaveBytes, a.AmountHave)
	buf = append(buf, amountHaveBytes[:]...)

	amountWantBytes := make([]byte, 8)
	binary.LittleEndian.PutUint64(amountWantBytes, a.AmountWant)
	buf = append(buf, amountWantBytes[:]...)

	lenSideBytes := make([]byte, 8)
	binary.LittleEndian.PutUint64(lenSideBytes, uint64(len(a.Side)))
	buf = append(buf, lenSideBytes[:]...)

	buf = append(buf, []byte(a.Side)...)
	buf = append(buf, a.AuctionID[:]...)
	buf = append(buf, a.Nonce[:]...)
	return
}

// Deserialize deserializes an order into the struct ptr it's being called on
func (a *AuctionOrder) Deserialize(data []byte) (err error) {
	// 33 for pubkey, 26 for the rest, 8 for len side, 4 for min side ("sell" is 4 bytes), 32 for auctionID, 2 for nonce, 8 for siglen
	// bucket is where we put all of the non byte stuff so we can get their length

	// TODO: remove all of this serialization code entirely and use protobufs or something else
	minimumDataLength := len(a.Nonce) +
		len(a.AuctionID) +
		binary.Size(a.OrderbookPrice) +
		binary.Size(a.AmountWant) +
		binary.Size(a.AmountHave) +
		a.TradingPair.Size() +
		len(a.Pubkey)
	if len(data) < minimumDataLength {
		err = fmt.Errorf("Auction order cannot be less than %d bytes: %s", len(data), err)
		return
	}

	copy(a.Pubkey[:], data[:33])
	data = data[33:]
	var tradingPairBytes [2]byte
	copy(tradingPairBytes[:], data[:2])
	if err = a.TradingPair.Deserialize(tradingPairBytes[:]); err != nil {
		err = fmt.Errorf("Could not deserialize trading pair while deserializing auction order: %s", err)
		return
	}
	data = data[2:]
	a.AmountHave = binary.LittleEndian.Uint64(data[:8])
	data = data[8:]
	a.AmountWant = binary.LittleEndian.Uint64(data[:8])
	data = data[8:]
	sideLen := binary.LittleEndian.Uint64(data[:8])
	data = data[8:]
	a.Side = string(data[:sideLen])
	data = data[sideLen:]
	copy(a.AuctionID[:], data[:32])
	data = data[32:]
	copy(a.Nonce[:], data[:2])
	data = data[2:]
	sigLen := binary.LittleEndian.Uint64(data[:8])
	data = data[8:]
	a.Signature = data[:sigLen]
	data = data[sigLen:]

	return
}

// SetAmountWant sets the amountwant value of the limit order according to a price
func (a *AuctionOrder) SetAmountWant(price float64) (err error) {
	if price <= 0 {
		err = fmt.Errorf("Price can't be less than or equal to 0")
		return
	}

	if a.IsBuySide() {
		a.AmountWant = uint64(float64(a.AmountHave) * price)
	} else if a.IsSellSide() {
		a.AmountWant = uint64(float64(a.AmountHave) / price)
	} else {
		err = fmt.Errorf("Invalid side for order, must be buy or sell")
		return
	}
	return
}

func (a *AuctionOrder) String() string {
	// we ignore error because there's nothing we can do in a String() method
	// to pass on the error other than panic, and I don't want to do that?
	orderMarshalled, _ := json.Marshal(a)
	return string(orderMarshalled)
}

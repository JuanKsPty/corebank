package ibkr

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
)

// sendRequestResponse is the small envelope both SendRequest and a
// not-yet-ready GetStatement return.
type sendRequestResponse struct {
	XMLName       xml.Name `xml:"FlexStatementResponse"`
	Status        string   `xml:"Status"`
	ReferenceCode string   `xml:"ReferenceCode"`
	ErrorCode     string   `xml:"ErrorCode"`
	ErrorMessage  string   `xml:"ErrorMessage"`
}

// statementDocument is a ready statement: IBKR's own report, not yet
// translated into this package's neutral Statement type. Field names mirror
// IBKR's Flex Query XML attributes.
type statementDocument struct {
	XMLName    xml.Name `xml:"FlexQueryResponse"`
	Statements []struct {
		AccountID string `xml:"accountId,attr"`
		// The window the report covers and when IBKR produced it. The client
		// never sends dates, so these are the only record of which period the
		// query's own saved settings actually returned.
		FromDate      string `xml:"fromDate,attr"`
		ToDate        string `xml:"toDate,attr"`
		WhenGenerated string `xml:"whenGenerated,attr"`
		// Pointers, so a section the query does not include at all (nil) can
		// be told apart from one that is present but empty.
		CashTransactions *struct {
			Items []cashTransactionXML `xml:"CashTransaction"`
		} `xml:"CashTransactions"`
		Trades *struct {
			Items []tradeXML `xml:"Trade"`
		} `xml:"Trades"`
		OpenPositions *struct {
			Items []openPositionXML `xml:"OpenPosition"`
		} `xml:"OpenPositions"`
	} `xml:"FlexStatements>FlexStatement"`
}

type cashTransactionXML struct {
	AccountID     string `xml:"accountId,attr"`
	Symbol        string `xml:"symbol,attr"`
	Currency      string `xml:"currency,attr"`
	Amount        string `xml:"amount,attr"`
	Type          string `xml:"type,attr"`
	Description   string `xml:"description,attr"`
	DateTime      string `xml:"dateTime,attr"`
	ReportDate    string `xml:"reportDate,attr"`
	TransactionID string `xml:"transactionID,attr"`
	// LevelOfDetail is "SUMMARY" or "DETAIL" — verified against a real
	// account: IBKR reports the *same* cash event twice, once at each
	// level, and only the DETAIL row carries a TransactionID. See
	// filterCashTransactions in parse.go for why this matters.
	LevelOfDetail string `xml:"levelOfDetail,attr"`
}

type tradeXML struct {
	AccountID     string `xml:"accountId,attr"`
	Symbol        string `xml:"symbol,attr"`
	Currency      string `xml:"currency,attr"`
	AssetCategory string `xml:"assetCategory,attr"`
	BuySell       string `xml:"buySell,attr"`
	Quantity      string `xml:"quantity,attr"`
	TradePrice    string `xml:"tradePrice,attr"`
	IBCommission  string `xml:"ibCommission,attr"`
	NetCash       string `xml:"netCash,attr"`
	TradeDate     string `xml:"tradeDate,attr"`
	TradeID       string `xml:"tradeID,attr"`
	// LevelOfDetail is "EXECUTION" for a normal fill. Not filtered on today
	// — only one trade was available to check against a real account, not
	// enough to confirm whether trades duplicate across levels the way
	// cash transactions and positions do — but captured so that becomes a
	// one-line fix instead of a schema change if it turns out they do.
	LevelOfDetail string `xml:"levelOfDetail,attr"`
}

type openPositionXML struct {
	AccountID      string `xml:"accountId,attr"`
	Symbol         string `xml:"symbol,attr"`
	Currency       string `xml:"currency,attr"`
	AssetCategory  string `xml:"assetCategory,attr"`
	Position       string `xml:"position,attr"`
	MarkPrice      string `xml:"markPrice,attr"`
	PositionValue  string `xml:"positionValue,attr"`
	CostBasisPrice string `xml:"costBasisPrice,attr"`
	ReportDate     string `xml:"reportDate,attr"`
	// LevelOfDetail is "SUMMARY" (one row per symbol) or "LOT" (one row per
	// tax lot within a symbol) — verified against a real account: several
	// symbols appeared twice, once at each level. See filterOpenPositions.
	LevelOfDetail string `xml:"levelOfDetail,attr"`
}

// decodeXML is xml.Unmarshal with the charset IBKR declares (windows-1252 in
// some accounts' reports) treated as an error rather than silently mojibaked
// — a report this package cannot decode faithfully must fail loudly, not
// import corrupted symbols or descriptions.
func decodeXML(body []byte, v any) error {
	dec := xml.NewDecoder(bytes.NewReader(body))
	dec.CharsetReader = func(charset string, input io.Reader) (io.Reader, error) {
		if charset == "UTF-8" || charset == "utf-8" || charset == "" {
			return input, nil
		}
		return nil, fmt.Errorf("ibkr: unsupported XML charset %q", charset)
	}
	return dec.Decode(v)
}

// looksLikeStatement distinguishes a ready statement from the small
// FlexStatementResponse envelope without fully parsing either, by checking
// which root element opens the document.
func looksLikeStatement(body []byte) bool {
	return bytes.Contains(body[:min(len(body), 256)], []byte("<FlexQueryResponse"))
}

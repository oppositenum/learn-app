package usage

import (
	"errors"
	"math/big"
)

type Price struct {
	InputPerMillion       string
	CachedInputPerMillion string
	OutputPerMillion      string
	AudioInputPerMinute   string
	AudioOutputPerMinute  string
}

type Amounts struct {
	InputTokens        int64
	CachedInputTokens  int64
	OutputTokens       int64
	AudioInputSeconds  string
	AudioOutputSeconds string
}

func Calculate(price Price, amounts Amounts) (*big.Rat, error) {
	if amounts.InputTokens < 0 || amounts.CachedInputTokens < 0 || amounts.OutputTokens < 0 || amounts.CachedInputTokens > amounts.InputTokens {
		return nil, errors.New("invalid token amounts")
	}
	inputPrice, err := decimal(price.InputPerMillion)
	if err != nil {
		return nil, err
	}
	cachedPrice, err := decimal(price.CachedInputPerMillion)
	if err != nil {
		return nil, err
	}
	outputPrice, err := decimal(price.OutputPerMillion)
	if err != nil {
		return nil, err
	}
	audioInputPrice, err := decimalOrZero(price.AudioInputPerMinute)
	if err != nil {
		return nil, err
	}
	audioOutputPrice, err := decimalOrZero(price.AudioOutputPerMinute)
	if err != nil {
		return nil, err
	}
	audioInputSeconds, err := decimalOrZero(amounts.AudioInputSeconds)
	if err != nil {
		return nil, err
	}
	audioOutputSeconds, err := decimalOrZero(amounts.AudioOutputSeconds)
	if err != nil {
		return nil, err
	}

	uncached := amounts.InputTokens - amounts.CachedInputTokens
	total := new(big.Rat)
	total.Add(total, tokenCost(uncached, inputPrice))
	total.Add(total, tokenCost(amounts.CachedInputTokens, cachedPrice))
	total.Add(total, tokenCost(amounts.OutputTokens, outputPrice))
	total.Add(total, new(big.Rat).Quo(new(big.Rat).Mul(audioInputSeconds, audioInputPrice), big.NewRat(60, 1)))
	total.Add(total, new(big.Rat).Quo(new(big.Rat).Mul(audioOutputSeconds, audioOutputPrice), big.NewRat(60, 1)))
	return total, nil
}

func tokenCost(tokens int64, price *big.Rat) *big.Rat {
	return new(big.Rat).Quo(new(big.Rat).Mul(big.NewRat(tokens, 1), price), big.NewRat(1_000_000, 1))
}

func decimal(value string) (*big.Rat, error) {
	if value == "" {
		return nil, errors.New("price is required")
	}
	result, ok := new(big.Rat).SetString(value)
	if !ok || result.Sign() < 0 {
		return nil, errors.New("invalid non-negative decimal")
	}
	return result, nil
}

func decimalOrZero(value string) (*big.Rat, error) {
	if value == "" {
		return new(big.Rat), nil
	}
	return decimal(value)
}

package llm

type Cost struct {
	Currency string  `json:"currency,omitempty"`
	Amount   float64 `json:"amount,omitempty"`
}

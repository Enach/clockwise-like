package calendar

type Provider interface {
	ListEvents() ([]string, error)
}

type Client struct{}

func (*Client) GetFreeBusy() (map[string]bool, error) { return nil, nil }

func NewClient() (*Client, error) { return &Client{}, nil }

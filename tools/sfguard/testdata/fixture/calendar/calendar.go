package calendar

type Provider interface {
	ListEvents() ([]string, error)
}

type Client struct{}

func (*Client) GetFreeBusy() (map[string]bool, error) { return nil, nil }

func NewClient() (*Client, error) { return &Client{}, nil }

func (*Client) GetEvent(id string) (string, error) { return id, nil }

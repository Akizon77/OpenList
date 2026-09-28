package immich

import (
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
)

type Addition struct {
	Endpoint string `json:"endpoint" required:"true" help:"Immich server URL, with or without /api"`
	APIKey   string `json:"api_key" required:"true"`
}

var config = driver.Config{
	Name:        "Immich",
	LocalSort:   true,
	OnlyProxy:   true,
	CheckStatus: true,
	// An asset can appear in several albums, so writes affect multiple directories.
	NoCache: true,
}

func init() {
	op.RegisterDriver(func() driver.Driver {
		return &Immich{}
	})
}

package stations

import (
	"testing"

	"github.com/yetone/magpie/internal/testenv"
)

// The routes write stations.yml and stations' folders: in a home of their
// own, never the reader's.
func TestMain(m *testing.M) { testenv.Main(m) }

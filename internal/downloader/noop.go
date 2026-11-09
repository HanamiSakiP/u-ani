package downloader

import "github.com/HanamiSakiP/u-ani/internal/logger"

type Noop struct{}

func (n *Noop) CheckConfig() {}

func (n *Noop) AddDownload(url, savePath string) (string, error) {
	logger.Info("noop downloader:\ndownloading url=%q\nsave_path=%q", url, savePath)
	return "", nil
}

func (n *Noop) GetStatus(id string) (Status, error) { return Downloading, nil }

func (n *Noop) GetFiles(id string) ([]string, error) { return nil, nil }

func (n *Noop) RemoveDownload(id string) error { return nil }

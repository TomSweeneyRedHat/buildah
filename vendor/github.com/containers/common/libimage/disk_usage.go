//go:build !remote

package libimage

import (
	"context"
	"time"
)

// ImageDiskUsage reports the total size of an image.  That is the size
type ImageDiskUsage struct {
	// Number of containers using the image.
	Containers int
	// ID of the image.
	ID string
	// Repository of the image.
	Repository string
	// Tag of the image.
	Tag string
	// Created time stamp.
	Created time.Time
	// The amount of space that an image shares with another one (i.e. their common data).
	SharedSize int64
	// The the amount of space that is only used by a given image.
	UniqueSize int64
	// Sum of shared an unique size.
	Size int64
}

// DiskUsage calculates the disk usage for each image in the local containers
// storage.  Note that a single image may yield multiple usage reports, one for
// each repository tag.
func (r *Runtime) DiskUsage(ctx context.Context) ([]ImageDiskUsage, int64, error) {
	images, layers, err := r.getImagesAndLayers()
	if err != nil {
		return nil, -1, err
	}

	layerTree, err := r.newLayerTreeFromData(images, layers)
	if err != nil {
		return nil, -1, err
	}

	var totalSize int64
	visitedImages := make(map[string]bool)
	visistedLayers := make(map[string]bool)

	var allUsages []ImageDiskUsage
	for _, image := range images {
		usages, err := diskUsageForImage(ctx, image, layerTree)
		if err != nil {
			return nil, -1, err
		}
		allUsages = append(allUsages, usages...)

		if _, ok := visitedImages[image.ID()]; ok {
			// Do not count an image twice
			continue
		}
		visitedImages[image.ID()] = true

		size, err := image.Size()
		if err != nil {
			return nil, -1, err
		}
		for _, layer := range layerTree.layersOf(image) {
			if _, ok := visistedLayers[layer.ID]; ok {
				// Do not count a layer twice, so remove its
				// size from the image size.
				size -= layer.UncompressedSize
				continue
			}
			visistedLayers[layer.ID] = true
		}
		totalSize += size
	}
	return allUsages, totalSize, err
}

// diskUsageForImage returns the disk-usage baseistics for the specified image.
func diskUsageForImage(ctx context.Context, image *Image, tree *layerTree) ([]ImageDiskUsage, error) {
	if err := image.isCorrupted(ctx, ""); err != nil {
		return nil, err
	}

	base := ImageDiskUsage{
		ID:         image.ID(),
		Created:    image.Created(),
		Repository: "<none>",
		Tag:        "<none>",
	}

	// Shared, unique and total size.
	parent, err := tree.parent(ctx, image)
	if err != nil {
		return nil, err
	}
	childIDs, err := tree.children(ctx, image, false)
	if err != nil {
		return nil, err
	}

	// Optimistically set unique size to the full size of the image.
	size, err := image.Size()
	if err != nil {
		return nil, err
	}
	base.UniqueSize = size

	if len(childIDs) > 0 {
		// If we have children, we share everything.
		base.SharedSize = base.UniqueSize
		base.UniqueSize = 0
	} else if parent != nil {
		// If we have no children but a parent, remove the parent
		// (shared) size from the unique one.
		size, err := parent.Size()
		if err != nil {
			return nil, err
		}
		base.UniqueSize -= size
		base.SharedSize = size
	}

	base.Size = base.SharedSize + base.UniqueSize

	// Number of containers using the image.
	containers, err := image.Containers()
	if err != nil {
		return nil, err
	}
	base.Containers = len(containers)

	repoTags, err := image.NamedRepoTags()
	if err != nil {
		return nil, err
	}

	if len(repoTags) == 0 {
		return []ImageDiskUsage{base}, nil
	}

	pairs, err := ToNameTagPairs(repoTags)
	if err != nil {
		return nil, err
	}

	results := make([]ImageDiskUsage, len(pairs))
	for i, pair := range pairs {
		res := base
		res.Repository = pair.Name
		res.Tag = pair.Tag
		results[i] = res
	}

	return results, nil
}

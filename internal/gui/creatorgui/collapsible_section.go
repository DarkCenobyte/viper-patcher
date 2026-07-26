package creatorgui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type collapsibleSection struct {
	button    *widget.Button
	detail    fyne.CanvasObject
	container *fyne.Container
	open      bool
	onChanged func(bool)
}

func newCollapsibleSection(title string, detail fyne.CanvasObject, onChanged func(bool)) *collapsibleSection {
	section := &collapsibleSection{
		detail:    detail,
		onChanged: onChanged,
	}
	section.detail.Hide()
	section.button = widget.NewButtonWithIcon(title, theme.NavigateNextIcon(), section.toggle)
	section.button.Alignment = widget.ButtonAlignLeading
	section.button.Importance = widget.LowImportance
	section.container = container.NewVBox(section.button, section.detail, widget.NewSeparator())
	return section
}

func (section *collapsibleSection) toggle() {
	section.open = !section.open
	if section.open {
		section.detail.Show()
		section.button.SetIcon(theme.MenuDropDownIcon())
	} else {
		section.detail.Hide()
		section.button.SetIcon(theme.NavigateNextIcon())
	}
	section.container.Refresh()
	if section.onChanged != nil {
		section.onChanged(section.open)
	}
}

func (section *collapsibleSection) Container() fyne.CanvasObject {
	return section.container
}

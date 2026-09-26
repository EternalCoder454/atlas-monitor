# What's new

Written for the people who use Atlas Monitor rather than the people who build
it. One short line per change, no jargon — this is what the update prompt shows.

## 0.10.0

- The window works at small sizes: the sidebar slides over the content, with a
  button to bring it back
- Right-click a column heading to hide it; the Columns button brings it back
- Hiding a column stops Atlas gathering that figure at all
- Page sections fold away, and a folded section is not updated either
- Service startup now reads On, Off or As needed
- Sidebar icons are larger

## 0.9.0

This is the first release of the Minimal build: the monitor on its own, with no
assistant, no model to download and nothing that talks to the network.

- Atlas samples every two seconds rather than every second, which halves what it
  costs to leave open
- It only gathers the per-process figures a visible column is showing
- Updates come from the Minimal branch and cannot be pointed at the full one
- Laptops with two batteries get a page for each, so you can see which one is
  being used
- The Apps list no longer cuts program names short
- The two disk columns that read zero on most machines are hidden; the Columns
  button brings them back
- A drive with nothing mounted says so, instead of showing an empty bar
- Charts now say what the top of the chart means
- The Memory page hides the swap section on machines with no swap
- Update problems explain themselves in plain words, with the technical detail
  folded away
- Chart lines are easier to see in the light theme
- Atlas can now be installed on Arch Linux with pacman

## 0.8.3

- The Apps list now reorders as you watch, so whatever is busiest stays at the top
- Programs using a lot of processor are highlighted in the list
- New icons throughout
- The sidebar now highlights the page you are actually on
- Speed graphs show their highest recent reading, so the scale means something

## 0.8.2

- Atlas now tells you when an update is available, with a list of what changed
- You can turn that check off in Settings
- The Apps page uses less memory when a lot of programs start and stop

## 0.8.1

- "End Task" can no longer close the wrong program by mistake
- Fixed updating from a folder whose name contains unusual characters
- Settings now warns you if the assistant would send your system details to
  another machine
- Your settings file is no longer readable by other users on the computer

## 0.8.0

- Fixed a memory leak on the Apps page that grew the longer it was open
- Opening Atlas twice no longer runs two copies at once
- Text is sharper, especially on 1080p screens
- Every processor core now shows its own percentage
- Storage shows how full each drive is
- The sidebar shows live processor, memory and graphics figures
- Quiet rows in the Apps list are dimmed so busy ones stand out
- The assistant now uses Qwen 3.5 9B by default

## 0.7.0

- Uses noticeably less processor time and fewer system reads while running

## 0.6.1

- New icons for the processor, memory, disk and graphics pages

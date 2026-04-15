# typed: true
# frozen_string_literal: true

# Source template for the marcelocantos/homebrew-tap formula.
# This file is updated by the /release skill when a new version ships.
# The tap at https://github.com/marcelocantos/homebrew-tap holds the live copy.
class Pageflip < Formula
  desc "Capture slides from a screen region whenever they change"
  homepage "https://github.com/marcelocantos/pageflip"
  version "0.1.0"
  license "Apache-2.0"

  on_macos do
    on_arm do
      url "https://github.com/marcelocantos/pageflip/releases/download/v0.1.0/pageflip-0.1.0-darwin-arm64.tar.gz"
      sha256 "<filled-by-release-bot>"
    end
  end

  on_linux do
    on_intel do
      url "https://github.com/marcelocantos/pageflip/releases/download/v0.1.0/pageflip-0.1.0-linux-amd64.tar.gz"
      sha256 "<filled-by-release-bot>"
    end

    on_arm do
      url "https://github.com/marcelocantos/pageflip/releases/download/v0.1.0/pageflip-0.1.0-linux-arm64.tar.gz"
      sha256 "<filled-by-release-bot>"
    end
  end

  def install
    bin.install "pageflip"
    bin.install "pageflip-feed"
  end

  test do
    system bin/"pageflip", "--version"
    system bin/"pageflip-feed", "--version"
  end
end

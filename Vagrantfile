# -*- mode: ruby -*-
# vi: set ft=ruby :

# Vagrantfile API/syntax version. Don't touch unless you know what you're doing!
VAGRANTFILE_API_VERSION = "2"

# shell script to bootstrap Go on Linux
GO_LINUX = 'go1.1.2.linux-386.tar.gz'
# Everything after /src/ in the host operating system's path
PROJECT_PATH = File.dirname(__FILE__).partition("#{File::SEPARATOR}src#{File::SEPARATOR}").last
PARENT_PATH = File.split(PROJECT_PATH).first

$bootstrap_linux = <<SCRIPT
apt-get update -qq
apt-get install -qq -y git mercurial bzr curl

if ! [ -f /home/vagrant/#{GO_LINUX} ]; then
  response=$(curl -O# https://go.googlecode.com/files/#{GO_LINUX})
fi
tar -C /usr/local -xzf #{GO_LINUX}

su vagrant -c "mkdir -p /home/vagrant/go/src/#{PARENT_PATH}"
su vagrant -c "ln -s /vagrant /home/vagrant/go/src/#{PROJECT_PATH}"

echo 'export GOPATH=$HOME/go' >> /home/vagrant/.profile
echo 'export PATH=$PATH:/usr/local/go/bin:$GOPATH/bin' >> /home/vagrant/.profile
echo 'cd $GOPATH/src/#{PROJECT_PATH}' >> /home/vagrant/.profile

su -l vagrant -c "go get -d -v ./..."

echo "\nRun: vagrant ssh -c 'go test -v ./...'"
SCRIPT

Vagrant.configure(VAGRANTFILE_API_VERSION) do |config|

  config.vm.define "linux" do |linux|
    # Every Vagrant virtual environment requires a box to build off of.
    linux.vm.box = "precise32"

    # The url from where the 'config.vm.box' box will be fetched if it
    # doesn't already exist on the user's system.
    linux.vm.box_url = "http://files.vagrantup.com/precise32.box"

    linux.vm.provision :shell, :inline => $bootstrap_linux
  end

end

# -*- mode: ruby -*-
# vi: set ft=ruby :

# Vagrantfile API/syntax version.
VAGRANTFILE_API_VERSION = "2"

INSTALL = {
  "linux" => "apt-get update -qq; apt-get install -qq -y git mercurial bzr curl",
  "bsd" => "pkg_add -r git mercurial"
}

GO_ARCHIVES = {
  "precise32" => "go1.1.2.linux-386.tar.gz",
  "freebsd32" => "go1.1.2.freebsd-386.tar.gz"
}

# shell script to bootstrap Go
def bootstrap(os, version)
  install = INSTALL[os]
  archive = GO_ARCHIVES[version]

  <<-SCRIPT
  #{install}

  if ! [ -f /home/vagrant/#{archive} ]; then
    response=$(curl -O# https://go.googlecode.com/files/#{archive})
  fi
  tar -C /usr/local -xzf #{archive}

  echo 'export GOPATH=$HOME/go' >> /home/vagrant/.profile
  echo 'export PATH=$PATH:/usr/local/go/bin:$GOPATH/bin' >> /home/vagrant/.profile
  echo 'cd $GOPATH/#{project_path}' >> /home/vagrant/.profile

  su -l vagrant -c "go get -d -v ./..."

  echo "\nRun: vagrant ssh #{os} -c 'go test ./...'"
  SCRIPT
end

# partition the path on the last /src/ element
def partition_path(path_to_project = File.dirname(__FILE__))
  path_elements = path_to_project.split(File::SEPARATOR)
  last_src = path_elements.rindex("src")
  raise "/src/ not found in #{path_to_project}" if last_src.nil?
  [File.join(path_elements.take(last_src)), File.join(path_elements.drop(last_src))]
end

# host operating system path to the last /src/
def src_path
  partition_path[0]
end

# path to the project
def project_path
  partition_path[1]
end

Vagrant.configure(VAGRANTFILE_API_VERSION) do |config|

  config.vm.define "linux" do |linux|
    linux.vm.box = "precise32"
    linux.vm.box_url = "http://files.vagrantup.com/precise32.box"
    linux.vm.synced_folder src_path, "/home/vagrant/go"
    linux.vm.provision :shell, :inline => bootstrap("linux", "precise32")
  end

  # Pete Cheslock's BSD box
  # https://gist.github.com/petecheslock/d7394ff93ce783c311c7
  # This box only supports NFS for synced/shared folders:
  # * which is unsupported on Windows hosts
  # * and requires a private host-only network
  # * and will prompt for the administrator password of the host
  config.vm.define "bsd" do |bsd|
    bsd.vm.box = "freebsd32"
    bsd.vm.box_url = "http://dyn-vm.s3.amazonaws.com/vagrant/dyn-virtualbox-freebsd-9.1-i386.box"
    bsd.vm.synced_folder ".", "/vagrant", :disabled => true
    bsd.vm.synced_folder src_path, "/home/vagrant/go", :nfs => true
    bsd.vm.network :private_network, :ip => '10.1.10.5'
    bsd.vm.provision :shell, :inline => bootstrap("bsd", "freebsd32")
  end

end
